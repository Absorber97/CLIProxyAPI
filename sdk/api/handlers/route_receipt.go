package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"

	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

const (
	RouteReceiptHeader        = "X-Cpa-Route-Receipt"
	RouteProviderHeader       = "X-Cpa-Route-Provider"
	RouteModelHeader          = "X-Cpa-Route-Model"
	RouteRequestedModelHeader = "X-Cpa-Route-Requested-Model"
	RouteSourceFormatHeader   = "X-Cpa-Route-Source-Format"
	RouteTargetFormatHeader   = "X-Cpa-Route-Target-Format"
	RouteAccountHeader        = "X-Cpa-Route-Account"
)

// routeReceipt names the upstream that served one request. Every field is
// observed after auth selection, never inferred from configuration.
type routeReceipt struct {
	Receipt        string
	Provider       string
	Model          string
	RequestedModel string
	SourceFormat   string
	TargetFormat   string
	Account        string
}

// routeReceiptCapture records the auth the scheduler actually selected. The
// conductor clones request metadata before execution, so a map write never
// reaches the caller and callbacks are the only channel back.
type routeReceiptCapture struct {
	mu       sync.Mutex
	provider string
	authID   string
}

// install adds capture callbacks without displacing any the caller already set.
func (c *routeReceiptCapture) install(meta map[string]any) {
	if meta == nil {
		return
	}
	previousProvider, _ := meta[coreexecutor.SelectedAuthProviderCallbackMetadataKey].(func(string))
	meta[coreexecutor.SelectedAuthProviderCallbackMetadataKey] = func(provider string) {
		c.mu.Lock()
		c.provider = provider
		c.mu.Unlock()
		if previousProvider != nil {
			previousProvider(provider)
		}
	}
	previousAuth, _ := meta[coreexecutor.SelectedAuthCallbackMetadataKey].(func(string))
	meta[coreexecutor.SelectedAuthCallbackMetadataKey] = func(authID string) {
		c.mu.Lock()
		c.authID = authID
		c.mu.Unlock()
		if previousAuth != nil {
			previousAuth(authID)
		}
	}
}

func (c *routeReceiptCapture) selected() (string, string) {
	if c == nil {
		return "", ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.provider, c.authID
}

func metadataString(meta map[string]any, key string) string {
	value, _ := meta[key].(string)
	return strings.TrimSpace(value)
}

// accountFingerprint identifies the serving account across requests without
// disclosing it, because auth IDs embed account addresses.
func accountFingerprint(authID string) string {
	if authID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(authID))
	return hex.EncodeToString(sum[:])[:16]
}

func newRouteReceipt(meta map[string]any, capture *routeReceiptCapture, receiptID, model, requestedModel, sourceFormat, targetFormat string) routeReceipt {
	provider, authID := capture.selected()
	if provider == "" {
		provider = metadataString(meta, coreexecutor.SelectedAuthProviderMetadataKey)
	}
	if authID == "" {
		authID = metadataString(meta, coreexecutor.SelectedAuthMetadataKey)
	}
	return routeReceipt{
		Receipt:        strings.TrimSpace(receiptID),
		Provider:       strings.TrimSpace(provider),
		Model:          strings.TrimSpace(model),
		RequestedModel: strings.TrimSpace(requestedModel),
		SourceFormat:   strings.TrimSpace(sourceFormat),
		TargetFormat:   strings.TrimSpace(targetFormat),
		Account:        accountFingerprint(strings.TrimSpace(authID)),
	}
}

// apply writes the receipt onto downstream response headers. A field the
// executor did not report is omitted, so a consumer requiring route evidence
// fails closed instead of reading a guessed value.
func (r routeReceipt) apply(headers http.Header) http.Header {
	if headers == nil {
		headers = make(http.Header)
	}
	for name, value := range map[string]string{
		RouteReceiptHeader:        r.Receipt,
		RouteProviderHeader:       r.Provider,
		RouteModelHeader:          r.Model,
		RouteRequestedModelHeader: r.RequestedModel,
		RouteSourceFormatHeader:   r.SourceFormat,
		RouteTargetFormatHeader:   r.TargetFormat,
		RouteAccountHeader:        r.Account,
	} {
		if value != "" && isPrintableHeaderValue(value) {
			headers.Set(name, value)
		}
	}
	return headers
}

func isPrintableHeaderValue(value string) bool {
	if len(value) > 200 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}
