package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
)

func TestRunPrintsDeterministicSecretSafeContract(t *testing.T) {
	const secret = "SECRET-SENTINEL-42"
	configPath := writeTestConfig(t, `
openai-compatibility:
  - name: omniroute
    base-url: https://user:`+secret+`@127.0.0.1:20128/v1?token=`+secret+`
    prefix: omni
    disabled: false
    support-prompt-cache-key: true
    disable-cooling: false
    request-retry: 0
    headers:
      X-Zeta: `+secret+`
      X-Alpha: safe
    api-key-entries:
      - api-key: `+secret+`
        weight: 3
        proxy-url: socks5://`+secret+`@127.0.0.1:1080
      - api-key: another-secret
        proxy-url: direct
    request-scoped-errors:
      - status: 429
        match: [`+secret+`, quota]
        match-regexr: [rate_limit]
        action: continue-and-cooldown
    models:
      - name: oak-gpt
        alias: omni-gpt-sol
        is-compat: true
      - name: oak-fable
        alias: omni-fable
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", configPath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}

	want := `{"schema_version":3,"openai_compatibility":[{"name":"omniroute","base_url":"https://127.0.0.1:20128","base_url_path_sha256":"2d234c97703ce824eaa4d98fbd2701668ef5e63e46f1574f2ea72e7927b1f57e","prefix":"omni","disabled":false,"api_key_count":2,"api_key_options":{"weighted_count":1,"proxy_count":1,"direct_count":1},"header_count":2,"header_names":["X-Alpha","X-Zeta"],"support_prompt_cache_key":true,"disable_cooling":false,"request_retry":0,"request_scoped_errors":{"rule_count":1,"match_count":2,"regex_count":1,"rules":[{"status":429,"action":"continue-and-cooldown","match_sha256":["3d57d53841036fac05563d4149f77d447e8ed01dd1e91a78bbc4a2cd89405321","b878a6801d9a9e68b30ed63430bb5e0bddcd984a37a3ee385abc27ff031c7fe7"],"regex_sha256":["3084e89ef570f74b12b5890eca52463c0642e061fb8646ab60257eb933836cdb"]}]},"model_count":2,"models":[{"name":"oak-gpt","alias":"omni-gpt-sol","is_compat":true,"thinking":null},{"name":"oak-fable","alias":"omni-fable","is_compat":false,"thinking":null}]}]}` + "\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout mismatch\n got: %s\nwant: %s", got, want)
	}
	if combined := stdout.String() + stderr.String(); strings.Contains(combined, secret) {
		t.Fatalf("output leaked secret sentinel: %q", combined)
	}
}

func TestRunPrintsRequestScopedRuleSemanticsWithoutMatchText(t *testing.T) {
	configPath := writeTestConfig(t, `
openai-compatibility:
  - name: omniroute
    base-url: http://127.0.0.1:20128/v1
    request-scoped-errors:
      - status: 429
        match-regexr: ["(?s).*"]
        action: stop
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", configPath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}

	const wantRule = `"rules":[{"status":429,"action":"stop","match_sha256":[],"regex_sha256":["f64f8bb74a7de14b61d04ab4395e1f3b992e3f1d3f6d54f45af7259dad62cf02"]}]`
	if !strings.Contains(stdout.String(), wantRule) {
		t.Fatalf("stdout missing request-scoped rule semantics: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), `(?s).*`) {
		t.Fatalf("stdout leaked request-scoped regex: %s", stdout.String())
	}
}

func TestRunPrintsSchemaV3RawThinkingCapabilities(t *testing.T) {
	configPath := writeTestConfig(t, `
openai-compatibility:
  - name: omniroute
    base-url: http://127.0.0.1:20128/v1
    models:
      - name: omni-gpt-sol
        alias: omni-gpt-sol
        is-compat: true
        thinking:
          min: 1
          max: 2
          zero-allowed: true
          dynamic-allowed: true
          levels: [low, medium, high, xhigh, max, ultra]
      - name: omni-fable
        alias: omni-fable
        thinking: null
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", configPath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}

	want := `{"schema_version":3,"openai_compatibility":[{"name":"omniroute","base_url":"http://127.0.0.1:20128","base_url_path_sha256":"2d234c97703ce824eaa4d98fbd2701668ef5e63e46f1574f2ea72e7927b1f57e","prefix":"","disabled":false,"api_key_count":0,"api_key_options":{"weighted_count":0,"proxy_count":0,"direct_count":0},"header_count":0,"header_names":[],"support_prompt_cache_key":false,"disable_cooling":null,"request_retry":null,"request_scoped_errors":{"rule_count":0,"match_count":0,"regex_count":0,"rules":[]},"model_count":2,"models":[{"name":"omni-gpt-sol","alias":"omni-gpt-sol","is_compat":true,"thinking":{"min":1,"max":2,"zero_allowed":true,"dynamic_allowed":true,"levels":["low","medium","high","xhigh","max","ultra"]}},{"name":"omni-fable","alias":"omni-fable","is_compat":false,"thinking":null}]}]}` + "\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestRunRejectsNonCanonicalThinkingLevelsWithoutLeakingValues(t *testing.T) {
	tests := []struct {
		name   string
		levels string
		value  string
	}{
		{name: "case variant", levels: "[Ultra]", value: "Ultra"},
		{name: "surrounding whitespace", levels: `[" high "]`, value: " high "},
		{name: "unknown secret sentinel", levels: "[SECRET-SENTINEL-THINKING]", value: "SECRET-SENTINEL-THINKING"},
		{name: "duplicate", levels: "[low, low]", value: "low, low"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configPath := writeTestConfig(t, `
openai-compatibility:
  - name: omniroute
    base-url: http://127.0.0.1:20128/v1
    models:
      - name: test-model
        alias: test-model
        thinking:
          levels: `+test.levels+`
`)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := run([]string{"--config", configPath}, &stdout, &stderr); exitCode == 0 {
				t.Fatal("run() accepted non-canonical thinking levels")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if combined := stdout.String() + stderr.String(); strings.Contains(combined, test.value) {
				t.Fatalf("error output leaked configured thinking level: %q", combined)
			}
		})
	}
}

func TestSummarizeConfigCopiesRawThinkingLevels(t *testing.T) {
	cfg, errParse := parseRuntimeConfig([]byte(`
openai-compatibility:
  - name: omniroute
    base-url: http://127.0.0.1:20128/v1
    models:
      - name: omni-gpt-sol
        alias: omni-gpt-sol
        thinking:
          levels: [ultra, high, none]
`))
	if errParse != nil {
		t.Fatal(errParse)
	}
	summary, errSummary := summarizeConfig(cfg)
	if errSummary != nil {
		t.Fatal(errSummary)
	}

	cfg.OpenAICompatibility[0].Models[0].Thinking.Levels[0] = "mutated"
	got := summary.OpenAICompatibility[0].Models[0].Thinking.Levels
	want := []string{"ultra", "high", "none"}
	if len(got) != len(want) {
		t.Fatalf("thinking levels = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("thinking levels = %#v, want %#v", got, want)
		}
	}
}

func TestSummarizeConfigKeepsExplicitEmptyThinkingDistinctFromNull(t *testing.T) {
	cfg, errParse := parseRuntimeConfig([]byte(`
openai-compatibility:
  - name: omniroute
    base-url: http://127.0.0.1:20128/v1
    models:
      - name: explicit-empty
        alias: explicit-empty
        thinking: {}
      - name: explicit-null
        alias: explicit-null
        thinking: null
`))
	if errParse != nil {
		t.Fatal(errParse)
	}
	summary, errSummary := summarizeConfig(cfg)
	if errSummary != nil {
		t.Fatal(errSummary)
	}

	empty := summary.OpenAICompatibility[0].Models[0].Thinking
	if empty == nil || empty.Levels == nil || len(empty.Levels) != 0 {
		t.Fatalf("explicit empty thinking = %#v, want non-nil object with empty levels", empty)
	}
	if got := summary.OpenAICompatibility[0].Models[1].Thinking; got != nil {
		t.Fatalf("explicit null thinking = %#v, want nil", got)
	}
}

func TestRunRequiresExplicitConfigAndDoesNotReadWorkingDirectory(t *testing.T) {
	workingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDir, "config.yaml"), []byte("openai-compatibility:\n  - base-url: https://SECRET-SENTINEL.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(workingDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run(nil, &stdout, &stderr); exitCode == 0 {
		t.Fatal("run() succeeded without --config")
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-SENTINEL") {
		t.Fatalf("command read the working directory config: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunRejectsInvalidConfigWithoutLeakingSecret(t *testing.T) {
	const secret = "SECRET-SENTINEL-INVALID"
	configPath := writeTestConfig(t, "openai-compatibility:\n  - api-key-entries: ["+secret+"\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", configPath}, &stdout, &stderr); exitCode == 0 {
		t.Fatal("run() succeeded for invalid YAML")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), secret) {
		t.Fatalf("error output leaked secret sentinel: %q", stdout.String()+stderr.String())
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	configPath := writeTestConfig(t, "{}\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", configPath, "extra"}, &stdout, &stderr); exitCode == 0 {
		t.Fatal("run() succeeded with a positional argument")
	}
}

func TestRunRejectsSingleDashConfigFlag(t *testing.T) {
	configPath := writeTestConfig(t, "{}\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"-config", configPath}, &stdout, &stderr); exitCode == 0 {
		t.Fatal("run() accepted -config instead of the pinned --config form")
	}
}

func TestRunUsesPinnedNullAndEmptyCollectionConventions(t *testing.T) {
	configPath := writeTestConfig(t, `
openai-compatibility:
  - name: empty-options
    base-url: http://127.0.0.1:20128/v1
    models: []
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", configPath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	want := `{"schema_version":3,"openai_compatibility":[{"name":"empty-options","base_url":"http://127.0.0.1:20128","base_url_path_sha256":"2d234c97703ce824eaa4d98fbd2701668ef5e63e46f1574f2ea72e7927b1f57e","prefix":"","disabled":false,"api_key_count":0,"api_key_options":{"weighted_count":0,"proxy_count":0,"direct_count":0},"header_count":0,"header_names":[],"support_prompt_cache_key":false,"disable_cooling":null,"request_retry":null,"request_scoped_errors":{"rule_count":0,"match_count":0,"regex_count":0,"rules":[]},"model_count":0,"models":[]}]}` + "\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestRunHashesBaseURLPathWithoutEmittingIt(t *testing.T) {
	const secret = "SECRET-SENTINEL-PATH"
	configPath := writeTestConfig(t, "openai-compatibility:\n  - name: path-test\n    base-url: https://EXAMPLE.com:8443/v1/"+secret+"?token="+secret+"\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", configPath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	got := stdout.String()
	if strings.Contains(got+stderr.String(), secret) {
		t.Fatalf("output leaked URL path or query: %q", got+stderr.String())
	}
	for _, want := range []string{`"base_url":"https://example.com:8443"`, `"base_url_path_sha256":"2ddd5e6e994ac0648a752e4cc7d4316f14521ad18289592d9a07f0fa068c9164"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want field %s", got, want)
		}
	}
}

func TestRunAppliesRuntimeLiveMediaRelayValidation(t *testing.T) {
	configPath := writeTestConfig(t, "codex:\n  live-media-relay:\n    enabled: true\n    public-ip: SECRET-SENTINEL-INVALID-IP\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", configPath}, &stdout, &stderr); exitCode == 0 {
		t.Fatal("run() accepted a config rejected by the runtime loader")
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-SENTINEL") {
		t.Fatalf("validation error leaked config value: %q", stdout.String()+stderr.String())
	}
}

func TestRunSuppressesProcessStderrParserLogs(t *testing.T) {
	const secret = "SECRET-SENTINEL-LOG"
	configPath := writeTestConfig(t, "redis-usage-queue-retention-seconds: 999999\napi-keys: ["+secret+"]\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	readPipe, writePipe, errPipe := os.Pipe()
	if errPipe != nil {
		t.Fatal(errPipe)
	}
	previousStderr := os.Stderr
	previousOutput := log.StandardLogger().Out
	previousLevel := log.GetLevel()
	t.Cleanup(func() {
		os.Stderr = previousStderr
		log.SetOutput(previousOutput)
		log.SetLevel(previousLevel)
		_ = readPipe.Close()
		_ = writePipe.Close()
	})
	os.Stderr = writePipe
	log.SetOutput(os.Stderr)
	log.SetLevel(log.DebugLevel)
	exitCode := run([]string{"--config", configPath}, &stdout, &stderr)
	if log.StandardLogger().Out != os.Stderr {
		t.Fatal("parser did not restore global logger output")
	}
	if got := log.GetLevel(); got != log.DebugLevel {
		t.Fatalf("parser restored logger level = %s, want debug", got)
	}
	if errClose := writePipe.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	os.Stderr = previousStderr
	log.SetOutput(previousOutput)
	processStderr, errRead := io.ReadAll(readPipe)
	if errRead != nil {
		t.Fatal(errRead)
	}
	if errClose := readPipe.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if len(processStderr) != 0 {
		t.Fatalf("parser wrote to process stderr: %q", processStderr)
	}
	if strings.Contains(stdout.String()+stderr.String()+string(processStderr), secret) {
		t.Fatal("output leaked planted sentinel")
	}
}

func TestRunRejectsConfigOutsideTemporaryDirectory(t *testing.T) {
	file, errCreate := os.CreateTemp(".", ".config-contract-outside-*")
	if errCreate != nil {
		t.Fatal(errCreate)
	}
	outsidePath := file.Name()
	if _, errWrite := file.WriteString("{}\n"); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errClose := file.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	t.Cleanup(func() { _ = os.Remove(outsidePath) })
	assertRunRejectedPath(t, outsidePath)
}

func TestRunRejectsSymlinkedConfig(t *testing.T) {
	target := writeTestConfig(t, "{}\n")
	link := filepath.Join(t.TempDir(), "staged-link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	assertRunRejectedPath(t, link)
}

func TestRunRejectsHardLinkedConfig(t *testing.T) {
	target := writeTestConfig(t, "{}\n")
	link := filepath.Join(t.TempDir(), "staged-hardlink.yaml")
	if err := os.Link(target, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	assertRunRejectedPath(t, link)
}

func TestReadStagedConfigRejectsPathSwapBeforeOpen(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "staged.yaml")
	replacement := filepath.Join(directory, "replacement.yaml")
	if err := os.WriteFile(path, []byte("safe: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("replacement: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errRead := readStagedConfigWithHooks(path, stagedReadHooks{
		afterValidation: func() {
			if err := os.Rename(path, path+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
		},
	})
	if errRead == nil {
		t.Fatal("readStagedConfigWithHooks() accepted a path swapped before open")
	}
}

func TestReadStagedConfigRejectsPathSwapAfterOpen(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "staged.yaml")
	replacement := filepath.Join(directory, "replacement.yaml")
	if err := os.WriteFile(path, []byte("safe: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("replacement: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errRead := readStagedConfigWithHooks(path, stagedReadHooks{
		afterOpen: func() {
			if err := os.Rename(path, path+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
		},
	})
	if errRead == nil {
		t.Fatal("readStagedConfigWithHooks() accepted a path swapped after open")
	}
}

func assertRunRejectedPath(t *testing.T, path string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run([]string{"--config", path}, &stdout, &stderr); exitCode == 0 {
		t.Fatalf("run() accepted unsafe staged path %q", path)
	}
}

func writeTestConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "staged.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
