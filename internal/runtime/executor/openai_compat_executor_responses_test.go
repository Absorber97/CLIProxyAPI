package executor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

const openAICompatResolvedModelInfoKey = "cliproxy.resolved_api_key_model_info"

const openAICompatAgentMessageRequest = `{"model":"omni-opus","input":[{"type":"agent_message","id":"amsg_1","author":"/root","recipient":"/root/worker","content":[{"type":"input_text","text":"Payload:\n"},{"type":"encrypted_content","encrypted_content":"delegated task"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn_1"}}]}`

func TestOpenAICompatExecutorResponsesUsesResponsesEndpoint(t *testing.T) {
	var upstreamPath string
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-opus","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
	request := []byte(`{"model":"omni-opus","input":[{"role":"user","content":"hello"}]}`)
	_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-opus",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if upstreamPath != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", upstreamPath)
	}
	if !gjson.GetBytes(upstreamBody, "input").IsArray() || gjson.GetBytes(upstreamBody, "messages").Exists() {
		t.Fatalf("upstream body is not Responses JSON: %s", upstreamBody)
	}
}

func TestOpenAICompatExecutorResponsesStreamPreservesResponsesEvents(t *testing.T) {
	var upstreamPath string
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"hello","item_id":"msg_1","output_index":0,"content_index":0}` + "\n\n"))
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"omni-opus","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	request := []byte(`{"model":"omni-opus","input":[{"role":"user","content":"hello"}],"stream":true}`)
	result, err := executor.ExecuteStream(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-opus",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Stream:          true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	var streamed strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error = %v", chunk.Err)
		}
		streamed.Write(chunk.Payload)
	}
	if upstreamPath != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", upstreamPath)
	}
	if !gjson.GetBytes(upstreamBody, "input").IsArray() || gjson.GetBytes(upstreamBody, "messages").Exists() || gjson.GetBytes(upstreamBody, "stream_options").Exists() {
		t.Fatalf("upstream stream body is not Responses JSON: %s", upstreamBody)
	}
	if !strings.Contains(streamed.String(), `"type":"response.output_text.delta"`) {
		t.Fatalf("Responses delta was not preserved: %s", streamed.String())
	}
	if strings.Contains(streamed.String(), "chat.completion.chunk") {
		t.Fatalf("stream was converted to Chat Completions: %s", streamed.String())
	}
}

func TestOpenAICompatExecutorResponsesStreamPreservesOmniRouteRouteComment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": omniroute-route-v1 {\"provider\":\"cx\",\"model\":\"gpt-5.6-sol\",\"request_id\":\"route-1\"}\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
	request := []byte(`{"model":"omni-gpt-sol","input":[{"role":"user","content":"hello"}],"stream":true}`)
	result, err := executor.ExecuteStream(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-gpt-sol",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Stream:          true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	var streamed strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error = %v", chunk.Err)
		}
		streamed.Write(chunk.Payload)
	}
	var routeLine string
	for _, line := range strings.Split(streamed.String(), "\n") {
		if strings.HasPrefix(line, cpaRouteCommentPrefix) {
			routeLine = line
			break
		}
	}
	if routeLine == "" {
		t.Fatalf("route comment was not preserved: %q", streamed.String())
	}
	var route map[string]string
	if errUnmarshal := json.Unmarshal([]byte(strings.TrimPrefix(routeLine, cpaRouteCommentPrefix)), &route); errUnmarshal != nil {
		t.Fatalf("route comment JSON is invalid: %v", errUnmarshal)
	}
	if route["provider"] != "cx" || route["model"] != "gpt-5.6-sol" || route["request_id"] != "route-1" {
		t.Fatalf("route comment = %#v", route)
	}
}

func TestOpenAICompatExecutorResponsesStreamAcceptsCompletedWithoutDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"hello","item_id":"msg_1","output_index":0,"content_index":0}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"omni-opus","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	request := []byte(`{"model":"omni-opus","input":[{"role":"user","content":"hello"}],"stream":true}`)
	result, err := executor.ExecuteStream(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-opus",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Stream:          true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	var streamed strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error = %v", chunk.Err)
		}
		streamed.Write(chunk.Payload)
	}
	if !strings.Contains(streamed.String(), `"type":"response.completed"`) {
		t.Fatalf("terminal Responses event was not preserved: %s", streamed.String())
	}
}

func TestOpenAICompatExecutorCompactStreamKeepsChatCompletionsEndpoint(t *testing.T) {
	var upstreamPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	request := []byte(`{"model":"omni-opus","input":"hello","stream":true}`)
	result, err := executor.ExecuteStream(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-opus",
		Payload: request,
	}, cliproxyexecutor.Options{
		Alt:             "responses/compact",
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Stream:          true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	for range result.Chunks {
	}
	if upstreamPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", upstreamPath)
	}
}

func TestOpenAICompatExecutorChatStillUsesChatCompletionsEndpoint(t *testing.T) {
	var upstreamPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":0,"model":"chat-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	request := []byte(`{"model":"chat-model","messages":[{"role":"user","content":"hello"}]}`)
	_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "chat-model",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAI,
		ResponseFormat:  sdktranslator.FormatOpenAI,
		OriginalRequest: request,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if upstreamPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", upstreamPath)
	}
}

func TestOpenAICompatExecutorResponsesPreservesOfficialCodexAgentMessageTask(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-opus","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{
		Codex: config.CodexConfig{OptimizeMultiAgentV2: true},
	})
	request := []byte(openAICompatAgentMessageRequest)
	_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-opus",
		Payload: request,
		Metadata: map[string]any{
			openAICompatResolvedModelInfoKey: &registry.ModelInfo{IsCompat: true},
		},
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Headers:         http.Header{"User-Agent": []string{"codex_cli_rs/0.145.0"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	message := gjson.GetBytes(upstreamBody, "input.0")
	if message.Get("type").String() != "message" || message.Get("role").String() != "user" {
		t.Fatalf("agent message was not normalized for portable Responses: %s", upstreamBody)
	}
	if message.Get("content.1.type").String() != "input_text" || message.Get("content.1.text").String() != "delegated task" {
		t.Fatalf("delegated task text was not preserved: %s", upstreamBody)
	}
	if message.Get("content.1.encrypted_content").Exists() {
		t.Fatalf("encrypted task wrapper reached the compatibility upstream: %s", upstreamBody)
	}
	for _, field := range []string{"id", "author", "recipient", "internal_chat_message_metadata_passthrough"} {
		if message.Get(field).Exists() {
			t.Fatalf("agent_message-only field %q reached portable Responses input: %s", field, upstreamBody)
		}
	}
}

func TestOpenAICompatExecutorResponsesAgentMessageRewriteGates(t *testing.T) {
	tests := []struct {
		name      string
		isCompat  bool
		optimized bool
		userAgent string
	}{
		{name: "isCompat false", optimized: true, userAgent: "codex_cli_rs/0.145.0"},
		{name: "optimizer false", isCompat: true, userAgent: "codex_cli_rs/0.145.0"},
		{name: "non-Codex User-Agent", isCompat: true, optimized: true, userAgent: "other-client/1.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-opus","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
			}))
			defer server.Close()

			executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{
				Codex: config.CodexConfig{OptimizeMultiAgentV2: test.optimized},
			})
			request := []byte(openAICompatAgentMessageRequest)
			modelInfo := &registry.ModelInfo{IsCompat: test.isCompat}
			_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
				Model:   "omni-opus",
				Payload: request,
				Metadata: map[string]any{
					openAICompatResolvedModelInfoKey: modelInfo,
				},
			}, cliproxyexecutor.Options{
				SourceFormat:    sdktranslator.FormatOpenAIResponse,
				ResponseFormat:  sdktranslator.FormatOpenAIResponse,
				OriginalRequest: request,
				Headers:         http.Header{"User-Agent": []string{test.userAgent}},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			message := gjson.GetBytes(upstreamBody, "input.0")
			if message.Get("type").String() != "agent_message" || message.Get("role").Exists() {
				t.Fatalf("agent message changed outside the official compat gate: %s", upstreamBody)
			}
			if message.Get("content.1.type").String() != "encrypted_content" || message.Get("content.1.encrypted_content").String() != "delegated task" {
				t.Fatalf("encrypted task changed outside the official compat gate: %s", upstreamBody)
			}
		})
	}
}

func openAICompatTestAuth(serverURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": serverURL + "/v1",
		"api_key":  "test",
	}}
}

const openAICompatTurnMetadataRequest = `{"model":"omni-gpt-web","input":[{"role":"user","content":"hello"}],"client_metadata":{"x-codex-turn-metadata":{"thread_id":"thread_abc123","turn_id":"turn_def456"}}}`

const openAICompatTurnEnvironmentRequest = `{"model":"omni-gpt-web","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context><cwd>/workspace</cwd><sandbox_mode>read-only</sandbox_mode></environment_context>"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn_def456"}},{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn_def456"}}],"client_metadata":{"x-codex-turn-metadata":{"thread_id":"thread_abc123","turn_id":"turn_def456"}}}`

var openAICompatIdentityCases = []struct {
	name           string
	incoming       http.Header
	wantUserAgent  string
	wantOriginator string
}{
	{
		name:           "official Codex CLI",
		incoming:       http.Header{"User-Agent": []string{"codex_cli_rs/0.145.0"}, "Originator": []string{"codex_cli_rs"}},
		wantUserAgent:  "codex_cli_rs/0.145.0",
		wantOriginator: "codex_cli_rs",
	},
	{
		name:           "official Codex Desktop",
		incoming:       http.Header{"User-Agent": []string{"Codex Desktop/0.5.2"}, "Originator": []string{"codex desktop"}},
		wantUserAgent:  "Codex Desktop/0.5.2",
		wantOriginator: "codex desktop",
	},
	{
		name:           "Originator-only evidence",
		incoming:       http.Header{"User-Agent": []string{"unknown-client/9.9"}, "Originator": []string{"codex-tui"}},
		wantUserAgent:  openAICompatDefaultUserAgent,
		wantOriginator: "codex-tui",
	},
	{
		name:           "official User-Agent does not prove an unrelated Originator",
		incoming:       http.Header{"User-Agent": []string{"codex_cli_rs/0.145.0"}, "Originator": []string{"vscode"}},
		wantUserAgent:  "codex_cli_rs/0.145.0",
		wantOriginator: "",
	},
	{
		name:           "generic client with non-Codex Originator",
		incoming:       http.Header{"User-Agent": []string{"some-client/1.0"}, "Originator": []string{"vscode"}},
		wantUserAgent:  openAICompatDefaultUserAgent,
		wantOriginator: "",
	},
	{
		name:           "no incoming identity",
		incoming:       nil,
		wantUserAgent:  openAICompatDefaultUserAgent,
		wantOriginator: "",
	},
}

func TestOpenAICompatExecutorResponsesForwardsNativeCodexIdentity(t *testing.T) {
	for _, test := range openAICompatIdentityCases {
		t.Run(test.name, func(t *testing.T) {
			var upstreamHeader http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamHeader = r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-gpt-web","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
			}))
			defer server.Close()

			executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
			request := []byte(`{"model":"omni-gpt-web","input":[{"role":"user","content":"hello"}]}`)
			_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
				Model:   "omni-gpt-web",
				Payload: request,
			}, cliproxyexecutor.Options{
				SourceFormat:    sdktranslator.FormatOpenAIResponse,
				ResponseFormat:  sdktranslator.FormatOpenAIResponse,
				OriginalRequest: request,
				Headers:         test.incoming,
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got := upstreamHeader.Get("User-Agent"); got != test.wantUserAgent {
				t.Fatalf("upstream User-Agent = %q, want %q", got, test.wantUserAgent)
			}
			if got := upstreamHeader.Get("Originator"); got != test.wantOriginator {
				t.Fatalf("upstream Originator = %q, want %q", got, test.wantOriginator)
			}
		})
	}
}

func TestOpenAICompatExecutorResponsesStreamForwardsNativeCodexIdentity(t *testing.T) {
	for _, test := range openAICompatIdentityCases {
		t.Run(test.name, func(t *testing.T) {
			var upstreamHeader http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamHeader = r.Header.Clone()
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"omni-gpt-web","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"))
			}))
			defer server.Close()

			executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
			request := []byte(`{"model":"omni-gpt-web","input":[{"role":"user","content":"hello"}],"stream":true}`)
			result, err := executor.ExecuteStream(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
				Model:   "omni-gpt-web",
				Payload: request,
			}, cliproxyexecutor.Options{
				SourceFormat:    sdktranslator.FormatOpenAIResponse,
				ResponseFormat:  sdktranslator.FormatOpenAIResponse,
				OriginalRequest: request,
				Stream:          true,
				Headers:         test.incoming,
			})
			if err != nil {
				t.Fatalf("ExecuteStream() error = %v", err)
			}
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					t.Fatalf("stream error = %v", chunk.Err)
				}
			}
			if got := upstreamHeader.Get("User-Agent"); got != test.wantUserAgent {
				t.Fatalf("upstream User-Agent = %q, want %q", got, test.wantUserAgent)
			}
			if got := upstreamHeader.Get("Originator"); got != test.wantOriginator {
				t.Fatalf("upstream Originator = %q, want %q", got, test.wantOriginator)
			}
		})
	}
}

func TestOpenAICompatExecutorResponsesCodexIdentityYieldsToCustomAuthHeaders(t *testing.T) {
	var upstreamHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-gpt-web","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	auth := openAICompatTestAuth(server.URL)
	auth.Attributes["header:User-Agent"] = "operator-override/1.0"
	executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
	request := []byte(`{"model":"omni-gpt-web","input":[{"role":"user","content":"hello"}]}`)
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "omni-gpt-web",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Headers:         http.Header{"User-Agent": []string{"codex_cli_rs/0.145.0"}, "Originator": []string{"codex_cli_rs"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := upstreamHeader.Get("User-Agent"); got != "operator-override/1.0" {
		t.Fatalf("custom auth header lost precedence: User-Agent = %q", got)
	}
}

func TestOpenAICompatExecutorChatCompletionsKeepsGenericIdentityForCodexClient(t *testing.T) {
	var upstreamHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":0,"model":"chat-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	request := []byte(`{"model":"chat-model","messages":[{"role":"user","content":"hello"}]}`)
	_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "chat-model",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAI,
		ResponseFormat:  sdktranslator.FormatOpenAI,
		OriginalRequest: request,
		Headers:         http.Header{"User-Agent": []string{"codex_cli_rs/0.145.0"}, "Originator": []string{"codex_cli_rs"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := upstreamHeader.Get("User-Agent"); got != openAICompatDefaultUserAgent {
		t.Fatalf("Chat Completions upstream User-Agent = %q, want %q", got, openAICompatDefaultUserAgent)
	}
	if got := upstreamHeader.Get("Originator"); got != "" {
		t.Fatalf("Chat Completions upstream Originator = %q, want empty", got)
	}
}

func TestOpenAICompatExecutorResponsesPreservesCodexTurnMetadata(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-gpt-web","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
	request := []byte(openAICompatTurnMetadataRequest)
	_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-gpt-web",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Headers:         http.Header{"User-Agent": []string{"codex_cli_rs/0.145.0"}, "Originator": []string{"codex_cli_rs"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := gjson.GetBytes([]byte(openAICompatTurnMetadataRequest), "client_metadata.x-codex-turn-metadata").Raw
	got := gjson.GetBytes(upstreamBody, "client_metadata.x-codex-turn-metadata").Raw
	if got != want {
		t.Fatalf("turn metadata = %s, want %s (body: %s)", got, want, upstreamBody)
	}
	if id := gjson.GetBytes(upstreamBody, "client_metadata.x-codex-turn-metadata.thread_id").String(); id != "thread_abc123" {
		t.Fatalf("thread_id = %q, want thread_abc123", id)
	}
	if id := gjson.GetBytes(upstreamBody, "client_metadata.x-codex-turn-metadata.turn_id").String(); id != "turn_def456" {
		t.Fatalf("turn_id = %q, want turn_def456", id)
	}
}

func TestOpenAICompatExecutorResponsesStampsNativeCodexPassthroughForVerifiedClient(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-gpt-web","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
	request := []byte(openAICompatTurnMetadataRequest)
	_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-gpt-web",
		Payload: request,
		Metadata: map[string]any{
			openAICompatResolvedModelInfoKey: &registry.ModelInfo{IsCompat: false},
		},
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Headers:         http.Header{"User-Agent": []string{"codex_exec/0.1.0"}, "Originator": []string{"codex_exec"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !gjson.GetBytes(upstreamBody, "_nativeCodexPassthrough").Bool() {
		t.Fatalf("_nativeCodexPassthrough missing or false; body=%s", upstreamBody)
	}
}

func TestOpenAICompatExecutorResponsesOmitsNativeCodexPassthroughWithoutCodexIdentity(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-gpt-web","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
	request := []byte(openAICompatTurnMetadataRequest)
	_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-gpt-web",
		Payload: request,
		Metadata: map[string]any{
			openAICompatResolvedModelInfoKey: &registry.ModelInfo{IsCompat: false},
		},
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Headers:         http.Header{"User-Agent": []string{"curl/8.7.1"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gjson.GetBytes(upstreamBody, "_nativeCodexPassthrough").Exists() {
		t.Fatalf("_nativeCodexPassthrough must stay unset without Codex identity; body=%s", upstreamBody)
	}
}

func TestOpenAICompatExecutorResponsesStreamStampsNativeCodexPassthroughForVerifiedClient(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"omni-gpt-web","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
	request := []byte(`{"model":"omni-gpt-web","input":[{"role":"user","content":"hello"}],"stream":true,"client_metadata":{"x-codex-turn-metadata":{"thread_id":"thread_abc123","turn_id":"turn_def456"}}}`)
	result, err := executor.ExecuteStream(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-gpt-web",
		Payload: request,
		Metadata: map[string]any{
			openAICompatResolvedModelInfoKey: &registry.ModelInfo{IsCompat: false},
		},
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Stream:          true,
		Headers:         http.Header{"User-Agent": []string{"codex_exec/0.1.0"}, "Originator": []string{"codex_exec"}},
	})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error = %v", chunk.Err)
		}
	}
	if !gjson.GetBytes(upstreamBody, "_nativeCodexPassthrough").Bool() {
		t.Fatalf("_nativeCodexPassthrough missing or false; body=%s", upstreamBody)
	}
}

func TestOpenAICompatExecutorResponsesPreservesNativeCodexTurnEnvironment(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"omni-gpt-web","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatible-codex-omniroute", &config.Config{})
	request := []byte(openAICompatTurnEnvironmentRequest)
	_, err := executor.Execute(context.Background(), openAICompatTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "omni-gpt-web",
		Payload: request,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		ResponseFormat:  sdktranslator.FormatOpenAIResponse,
		OriginalRequest: request,
		Headers:         http.Header{"User-Agent": []string{"codex_cli_rs/0.145.0"}, "Originator": []string{"codex_cli_rs"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := gjson.GetBytes(request, "input").Raw
	got := gjson.GetBytes(upstreamBody, "input").Raw
	if got != want {
		t.Fatalf("native turn environment changed: got %s, want %s", got, want)
	}
}
