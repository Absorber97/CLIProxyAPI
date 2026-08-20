package responses

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIResponsesRequestToClaude_AssistantTurnNeverEndsOnThinking(t *testing.T) {
	rawSignature, _ := testClaudeResponsesThinkingSignature(t)
	raw := []byte(`{
		"model":"claude-test",
		"input":[
			{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"spawn a subagent"}]
			},
			{
				"type":"reasoning",
				"encrypted_content":"` + rawSignature + `",
				"summary":[{"type":"summary_text","text":"planning the delegation"}]
			},
			{
				"type":"message",
				"role":"assistant",
				"content":[{"type":"output_text","text":"delegating now"}]
			},
			{
				"type":"reasoning",
				"encrypted_content":"` + rawSignature + `",
				"summary":[{"type":"summary_text","text":"waiting on the subagent"}]
			},
			{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"continue"}]
			}
		]
	}`)

	out := ConvertOpenAIResponsesRequestToClaude("claude-test", raw, false)
	root := gjson.ParseBytes(out)

	root.Get("messages").ForEach(func(index, message gjson.Result) bool {
		if message.Get("role").String() != "assistant" {
			return true
		}
		content := message.Get("content")
		if !content.IsArray() {
			return true
		}
		blocks := content.Array()
		if last := blocks[len(blocks)-1].Get("type").String(); last == "thinking" || last == "redacted_thinking" {
			t.Fatalf("assistant message %s ends on %s. Output: %s", index.String(), last, string(out))
		}
		return true
	})

	if got := root.Get("messages.1.content.0.thinking").String(); got != "planning the delegation" {
		t.Fatalf("thinking before assistant text = %q, want it preserved. Output: %s", got, string(out))
	}
}

func TestConvertOpenAIResponsesRequestToClaude_KeepsThinkingBeforeToolUse(t *testing.T) {
	rawSignature, _ := testClaudeResponsesThinkingSignature(t)
	raw := []byte(`{
		"model":"claude-test",
		"input":[
			{
				"type":"reasoning",
				"encrypted_content":"` + rawSignature + `",
				"summary":[{"type":"summary_text","text":"choosing a tool"}]
			},
			{
				"type":"function_call",
				"call_id":"call_1",
				"name":"read_file",
				"arguments":"{\"path\":\"main.go\"}"
			},
			{
				"type":"function_call_output",
				"call_id":"call_1",
				"output":"package main"
			}
		]
	}`)

	out := ConvertOpenAIResponsesRequestToClaude("claude-test", raw, false)
	root := gjson.ParseBytes(out)

	if got := root.Get("messages.0.content.0.type").String(); got != "thinking" {
		t.Fatalf("first block type = %q, want thinking. Output: %s", got, string(out))
	}
	if got := root.Get("messages.0.content.1.type").String(); got != "tool_use" {
		t.Fatalf("second block type = %q, want tool_use. Output: %s", got, string(out))
	}
}
