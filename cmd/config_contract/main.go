package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	log "github.com/sirupsen/logrus"
)

const contractSchemaVersion = 3

var configParseLogMu sync.Mutex

type contractSummary struct {
	SchemaVersion       int                   `json:"schema_version"`
	OpenAICompatibility []openAICompatSummary `json:"openai_compatibility"`
}

type openAICompatSummary struct {
	Name                  string                    `json:"name"`
	BaseURL               string                    `json:"base_url"`
	BaseURLPathSHA256     string                    `json:"base_url_path_sha256"`
	Prefix                string                    `json:"prefix"`
	Disabled              bool                      `json:"disabled"`
	APIKeyCount           int                       `json:"api_key_count"`
	APIKeyOptions         apiKeyOptionsSummary      `json:"api_key_options"`
	HeaderCount           int                       `json:"header_count"`
	HeaderNames           []string                  `json:"header_names"`
	SupportPromptCacheKey bool                      `json:"support_prompt_cache_key"`
	DisableCooling        *bool                     `json:"disable_cooling"`
	RequestRetry          *int                      `json:"request_retry"`
	RequestScopedErrors   requestScopedErrorSummary `json:"request_scoped_errors"`
	ModelCount            int                       `json:"model_count"`
	Models                []modelSummary            `json:"models"`
}

type apiKeyOptionsSummary struct {
	WeightedCount int `json:"weighted_count"`
	ProxyCount    int `json:"proxy_count"`
	DirectCount   int `json:"direct_count"`
}

type requestScopedErrorSummary struct {
	RuleCount  int                             `json:"rule_count"`
	MatchCount int                             `json:"match_count"`
	RegexCount int                             `json:"regex_count"`
	Rules      []requestScopedErrorRuleSummary `json:"rules"`
}

type requestScopedErrorRuleSummary struct {
	Status      int      `json:"status"`
	Action      string   `json:"action"`
	MatchSHA256 []string `json:"match_sha256"`
	RegexSHA256 []string `json:"regex_sha256"`
}

type modelSummary struct {
	Name     string           `json:"name"`
	Alias    string           `json:"alias"`
	IsCompat bool             `json:"is_compat"`
	Thinking *thinkingSummary `json:"thinking"`
}

type thinkingSummary struct {
	Min            int      `json:"min"`
	Max            int      `json:"max"`
	ZeroAllowed    bool     `json:"zero_allowed"`
	DynamicAllowed bool     `json:"dynamic_allowed"`
	Levels         []string `json:"levels"`
}

func isCanonicalPublicThinkingLevel(level string) bool {
	switch level {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "auto":
		return true
	default:
		return false
	}
}

type stagedReadHooks struct {
	afterValidation func()
	afterOpen       func()
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "--config" || strings.TrimSpace(args[1]) == "" {
		_, _ = fmt.Fprintln(stderr, "usage: config_contract --config <staged-copy>")
		return 2
	}

	data, errRead := readStagedConfig(args[1])
	if errRead != nil {
		_, _ = fmt.Fprintln(stderr, "invalid staged config path")
		return 1
	}
	cfg, errParse := parseRuntimeConfig(data)
	if errParse != nil {
		_, _ = fmt.Fprintln(stderr, "invalid staged config")
		return 1
	}

	summary, errSummary := summarizeConfig(cfg)
	if errSummary != nil {
		_, _ = fmt.Fprintln(stderr, errSummary.Error())
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if errEncode := encoder.Encode(summary); errEncode != nil {
		_, _ = fmt.Fprintln(stderr, "cannot encode config contract")
		return 1
	}
	return 0
}

func readStagedConfig(path string) ([]byte, error) {
	return readStagedConfigWithHooks(path, stagedReadHooks{})
}

func readStagedConfigWithHooks(path string, hooks stagedReadHooks) (data []byte, resultErr error) {
	info, errLstat := os.Lstat(path)
	if errLstat != nil {
		return nil, fmt.Errorf("invalid staged config file")
	}
	if errValidate := validateStagedFileInfo(info); errValidate != nil {
		return nil, errValidate
	}

	resolvedTemp, errTemp := filepath.EvalSymlinks(os.TempDir())
	if errTemp != nil {
		return nil, fmt.Errorf("resolve temporary directory")
	}
	resolvedPath, errResolve := filepath.EvalSymlinks(path)
	if errResolve != nil {
		return nil, fmt.Errorf("resolve staged config")
	}
	resolvedPath, errResolve = filepath.Abs(resolvedPath)
	if errResolve != nil {
		return nil, fmt.Errorf("resolve staged config")
	}
	resolvedTemp, errTemp = filepath.Abs(resolvedTemp)
	if errTemp != nil {
		return nil, fmt.Errorf("resolve temporary directory")
	}
	relative, errRelative := filepath.Rel(resolvedTemp, resolvedPath)
	if errRelative != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("staged config is outside the temporary directory")
	}

	if hooks.afterValidation != nil {
		hooks.afterValidation()
	}
	file, errOpen := openStagedFileNoFollow(resolvedPath)
	if errOpen != nil {
		return nil, fmt.Errorf("open staged config")
	}
	defer func() {
		if errClose := file.Close(); resultErr == nil && errClose != nil {
			resultErr = fmt.Errorf("close staged config")
		}
	}()

	openedInfo, errStat := file.Stat()
	if errStat != nil {
		return nil, fmt.Errorf("inspect opened staged config")
	}
	if errValidate := validateStagedFileInfo(openedInfo); errValidate != nil {
		return nil, errValidate
	}
	if !sameFileSnapshot(info, openedInfo) {
		return nil, fmt.Errorf("staged config changed before open")
	}
	if hooks.afterOpen != nil {
		hooks.afterOpen()
	}

	data, errRead := io.ReadAll(file)
	if errRead != nil {
		return nil, fmt.Errorf("read staged config")
	}
	finalInfo, errFinalStat := file.Stat()
	if errFinalStat != nil {
		return nil, fmt.Errorf("reinspect opened staged config")
	}
	if errValidate := validateStagedFileInfo(finalInfo); errValidate != nil {
		return nil, errValidate
	}
	if !sameFileSnapshot(openedInfo, finalInfo) {
		return nil, fmt.Errorf("staged config changed while reading")
	}

	finalPathInfo, errFinalLstat := os.Lstat(path)
	if errFinalLstat != nil {
		return nil, fmt.Errorf("reinspect staged config path")
	}
	if errValidate := validateStagedFileInfo(finalPathInfo); errValidate != nil {
		return nil, errValidate
	}
	if !sameFileSnapshot(openedInfo, finalPathInfo) {
		return nil, fmt.Errorf("staged config path changed while reading")
	}
	finalResolvedPath, errFinalResolve := filepath.EvalSymlinks(path)
	if errFinalResolve != nil {
		return nil, fmt.Errorf("reresolve staged config")
	}
	finalResolvedPath, errFinalResolve = filepath.Abs(finalResolvedPath)
	if errFinalResolve != nil || finalResolvedPath != resolvedPath {
		return nil, fmt.Errorf("staged config resolution changed while reading")
	}
	return data, nil
}

func validateStagedFileInfo(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("invalid staged config file")
	}
	count, available := fileLinkCount(info)
	if !available {
		return fmt.Errorf("staged config link count unavailable")
	}
	if count != 1 {
		return fmt.Errorf("staged config must have one filesystem link")
	}
	return nil
}

func sameFileSnapshot(left, right os.FileInfo) bool {
	return os.SameFile(left, right) &&
		left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime()) &&
		left.Mode() == right.Mode()
}

func fileLinkCount(info os.FileInfo) (uint64, bool) {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return 0, false
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0, false
	}
	field := value.FieldByName("Nlink")
	if !field.IsValid() {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return field.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		count := field.Int()
		if count < 0 {
			return 0, false
		}
		return uint64(count), true
	default:
		return 0, false
	}
}

func parseRuntimeConfig(data []byte) (*config.Config, error) {
	configParseLogMu.Lock()
	logger := log.StandardLogger()
	previousOutput := logger.Out
	previousLevel := logger.GetLevel()
	logger.SetOutput(io.Discard)
	logger.SetLevel(log.PanicLevel)
	defer func() {
		logger.SetLevel(previousLevel)
		logger.SetOutput(previousOutput)
		configParseLogMu.Unlock()
	}()

	cfg, errParse := config.ParseConfigBytes(data)
	if errParse != nil {
		return nil, errParse
	}
	if errValidate := cfg.Codex.LiveMediaRelay.Validate(); errValidate != nil {
		return nil, errValidate
	}
	return cfg, nil
}

func summarizeConfig(cfg *config.Config) (contractSummary, error) {
	entries := make([]openAICompatSummary, 0, len(cfg.OpenAICompatibility))
	for index := range cfg.OpenAICompatibility {
		entry, errSummary := summarizeOpenAICompat(index, cfg.OpenAICompatibility[index])
		if errSummary != nil {
			return contractSummary{}, errSummary
		}
		entries = append(entries, entry)
	}
	return contractSummary{
		SchemaVersion:       contractSchemaVersion,
		OpenAICompatibility: entries,
	}, nil
}

func summarizeOpenAICompat(index int, entry config.OpenAICompatibility) (openAICompatSummary, error) {
	baseURL, baseURLPathSHA256, errURL := secretSafeBaseURL(entry.BaseURL)
	if errURL != nil {
		return openAICompatSummary{}, fmt.Errorf("openai compatibility entry %d has invalid base URL", index)
	}

	keyOptions := apiKeyOptionsSummary{}
	for keyIndex := range entry.APIKeyEntries {
		key := entry.APIKeyEntries[keyIndex]
		if key.Weight != nil {
			keyOptions.WeightedCount++
		}
		proxyURL := strings.TrimSpace(key.ProxyURL)
		if strings.EqualFold(proxyURL, "direct") {
			keyOptions.DirectCount++
		} else if proxyURL != "" {
			keyOptions.ProxyCount++
		}
	}

	headerNames := make([]string, 0, len(entry.Headers))
	for name := range entry.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)

	errorCounts := requestScopedErrorSummary{
		RuleCount: len(entry.RequestScopedErrors),
		Rules:     make([]requestScopedErrorRuleSummary, 0, len(entry.RequestScopedErrors)),
	}
	for ruleIndex := range entry.RequestScopedErrors {
		rule := entry.RequestScopedErrors[ruleIndex]
		errorCounts.MatchCount += len(rule.Match)
		errorCounts.RegexCount += len(rule.MatchRegexr)
		matchHashes := make([]string, 0, len(rule.Match))
		for _, value := range rule.Match {
			hash := sha256.Sum256([]byte(value))
			matchHashes = append(matchHashes, fmt.Sprintf("%x", hash))
		}
		regexHashes := make([]string, 0, len(rule.MatchRegexr))
		for _, value := range rule.MatchRegexr {
			hash := sha256.Sum256([]byte(value))
			regexHashes = append(regexHashes, fmt.Sprintf("%x", hash))
		}
		sort.Strings(matchHashes)
		sort.Strings(regexHashes)
		errorCounts.Rules = append(errorCounts.Rules, requestScopedErrorRuleSummary{
			Status:      rule.Status,
			Action:      rule.Action,
			MatchSHA256: matchHashes,
			RegexSHA256: regexHashes,
		})
	}

	models := make([]modelSummary, 0, len(entry.Models))
	for modelIndex := range entry.Models {
		model := entry.Models[modelIndex]
		var thinking *thinkingSummary
		if model.Thinking != nil {
			levels := make([]string, 0, len(model.Thinking.Levels))
			seenLevels := make(map[string]struct{}, len(model.Thinking.Levels))
			for _, level := range model.Thinking.Levels {
				if !isCanonicalPublicThinkingLevel(level) {
					return openAICompatSummary{}, fmt.Errorf(
						"openai compatibility entry %d model %d has invalid thinking levels",
						index,
						modelIndex,
					)
				}
				if _, duplicate := seenLevels[level]; duplicate {
					return openAICompatSummary{}, fmt.Errorf(
						"openai compatibility entry %d model %d has invalid thinking levels",
						index,
						modelIndex,
					)
				}
				seenLevels[level] = struct{}{}
				levels = append(levels, level)
			}
			thinking = &thinkingSummary{
				Min:            model.Thinking.Min,
				Max:            model.Thinking.Max,
				ZeroAllowed:    model.Thinking.ZeroAllowed,
				DynamicAllowed: model.Thinking.DynamicAllowed,
				Levels:         levels,
			}
		}
		models = append(models, modelSummary{
			Name:     model.Name,
			Alias:    model.Alias,
			IsCompat: model.IsCompat,
			Thinking: thinking,
		})
	}

	return openAICompatSummary{
		Name:                  entry.Name,
		BaseURL:               baseURL,
		BaseURLPathSHA256:     baseURLPathSHA256,
		Prefix:                entry.Prefix,
		Disabled:              entry.Disabled,
		APIKeyCount:           len(entry.APIKeyEntries),
		APIKeyOptions:         keyOptions,
		HeaderCount:           len(headerNames),
		HeaderNames:           headerNames,
		SupportPromptCacheKey: entry.SupportPromptCacheKey,
		DisableCooling:        entry.DisableCooling,
		RequestRetry:          entry.RequestRetry,
		RequestScopedErrors:   errorCounts,
		ModelCount:            len(models),
		Models:                models,
	}, nil
}

func secretSafeBaseURL(raw string) (string, string, error) {
	parsed, errParse := url.Parse(raw)
	if errParse != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("invalid base URL")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", "", fmt.Errorf("invalid base URL")
	}
	host := hostname
	if port := parsed.Port(); port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	origin := strings.ToLower(parsed.Scheme) + "://" + host
	pathHash := sha256.Sum256([]byte(parsed.EscapedPath()))
	return origin, fmt.Sprintf("%x", pathHash), nil
}
