package handlers

import (
	"net/http"
	"testing"

	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestRouteReceiptReportsObservedUpstream(t *testing.T) {
	meta := map[string]any{
		coreexecutor.SelectedAuthProviderMetadataKey: "claude",
		coreexecutor.SelectedAuthMetadataKey:         "claude-user@example.com",
	}
	headers := newRouteReceipt(meta, nil, "req-1", "claude-opus-5", "claude-opus-5", "openai-responses", "openai-responses").apply(nil)

	for name, want := range map[string]string{
		RouteReceiptHeader:        "req-1",
		RouteProviderHeader:       "claude",
		RouteModelHeader:          "claude-opus-5",
		RouteRequestedModelHeader: "claude-opus-5",
		RouteSourceFormatHeader:   "openai-responses",
		RouteTargetFormatHeader:   "openai-responses",
	} {
		if got := headers.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := headers.Get(RouteAccountHeader); len(got) != 16 {
		t.Fatalf("account fingerprint = %q, want 16 hex characters", got)
	}
}

func TestRouteReceiptNeverDisclosesTheAccount(t *testing.T) {
	authID := "claude-user@example.com"
	meta := map[string]any{coreexecutor.SelectedAuthMetadataKey: authID}
	headers := newRouteReceipt(meta, nil, "req-2", "m", "m", "s", "t").apply(nil)
	for _, value := range headers {
		for _, entry := range value {
			if entry == authID {
				t.Fatal("route receipt disclosed the raw auth id")
			}
		}
	}
	same := newRouteReceipt(meta, nil, "req-3", "m", "m", "s", "t").apply(nil)
	if headers.Get(RouteAccountHeader) != same.Get(RouteAccountHeader) {
		t.Fatal("same account produced different fingerprints")
	}
}

func TestRouteReceiptOmitsUnobservedFields(t *testing.T) {
	headers := newRouteReceipt(map[string]any{}, nil, "", "claude-opus-5", "", "", "").apply(nil)
	for _, name := range []string{RouteReceiptHeader, RouteProviderHeader, RouteAccountHeader} {
		if got := headers.Get(name); got != "" {
			t.Fatalf("%s = %q, want omitted so the consumer fails closed", name, got)
		}
	}
	if got := headers.Get(RouteModelHeader); got != "claude-opus-5" {
		t.Fatalf("observed model was dropped: %q", got)
	}
}

func TestRouteReceiptRejectsUnsafeHeaderValues(t *testing.T) {
	meta := map[string]any{coreexecutor.SelectedAuthProviderMetadataKey: "claude\r\nX-Injected: 1"}
	headers := newRouteReceipt(meta, nil, "req-4", "m", "m", "s", "t").apply(nil)
	if got := headers.Get(RouteProviderHeader); got != "" {
		t.Fatalf("provider = %q, want omitted", got)
	}
	if headers.Get("X-Injected") != "" {
		t.Fatal("header injection succeeded")
	}
}

func TestRouteReceiptHeadersAreReserved(t *testing.T) {
	for _, name := range []string{
		RouteReceiptHeader, RouteProviderHeader, RouteModelHeader,
		RouteRequestedModelHeader, RouteSourceFormatHeader,
		RouteTargetFormatHeader, RouteAccountHeader,
	} {
		if !IsCPAReservedResponseHeader(name) {
			t.Fatalf("%s is not reserved, so an upstream could forge route evidence", name)
		}
	}
	if IsCPAReservedResponseHeader(http.CanonicalHeaderKey("x-not-a-route-header")) {
		t.Fatal("unrelated header reported as reserved")
	}
}

func TestRouteReceiptCaptureSurvivesMetadataCloning(t *testing.T) {
	meta := map[string]any{}
	capture := &routeReceiptCapture{}
	capture.install(meta)

	clone := map[string]any{}
	for k, v := range meta {
		clone[k] = v
	}
	clone[coreexecutor.SelectedAuthProviderCallbackMetadataKey].(func(string))("claude")
	clone[coreexecutor.SelectedAuthCallbackMetadataKey].(func(string))("claude-user@example.com")

	headers := newRouteReceipt(meta, capture, "req-5", "m", "m", "s", "t").apply(nil)
	if got := headers.Get(RouteProviderHeader); got != "claude" {
		t.Fatalf("provider = %q, want claude captured through the clone", got)
	}
	if got := headers.Get(RouteAccountHeader); len(got) != 16 {
		t.Fatalf("account fingerprint = %q, want 16 hex characters", got)
	}
}

func TestRouteReceiptCaptureKeepsExistingCallbacks(t *testing.T) {
	var seen []string
	meta := map[string]any{
		coreexecutor.SelectedAuthCallbackMetadataKey: func(id string) { seen = append(seen, id) },
	}
	capture := &routeReceiptCapture{}
	capture.install(meta)
	meta[coreexecutor.SelectedAuthCallbackMetadataKey].(func(string))("auth-1")

	if len(seen) != 1 || seen[0] != "auth-1" {
		t.Fatalf("pre-existing callback was displaced: %v", seen)
	}
	if _, authID := capture.selected(); authID != "auth-1" {
		t.Fatalf("capture missed the auth id: %q", authID)
	}
}
