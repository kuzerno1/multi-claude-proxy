package copilot

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

func TestTranslateToOpenAIResponses_BasicMessage(t *testing.T) {
	req := &types.AnthropicRequest{
		Model:     "gpt-5.2-codex",
		MaxTokens: 100,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
		},
	}

	result, err := TranslateToOpenAIResponses(req)
	if err != nil {
		t.Fatalf("TranslateToOpenAIResponses() error: %v", err)
	}

	if result.Model != "gpt-5.2-codex" {
		t.Errorf("expected model='gpt-5.2-codex', got %q", result.Model)
	}
	if result.MaxOutputTokens != 100 {
		t.Errorf("expected max_output_tokens=100, got %d", result.MaxOutputTokens)
	}
	if len(result.Input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(result.Input))
	}
	if result.Input[0].Type != "message" {
		t.Errorf("expected type='message', got %q", result.Input[0].Type)
	}
	if result.Input[0].Role != "user" {
		t.Errorf("expected role='user', got %q", result.Input[0].Role)
	}
}

func TestTranslateToOpenAIResponses_WithSystemPrompt(t *testing.T) {
	req := &types.AnthropicRequest{
		Model:     "gpt-5.2-codex",
		MaxTokens: 100,
		System:    json.RawMessage(`"You are helpful"`),
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
		},
	}

	result, err := TranslateToOpenAIResponses(req)
	if err != nil {
		t.Fatalf("TranslateToOpenAIResponses() error: %v", err)
	}

	if result.Instructions != "You are helpful" {
		t.Errorf("expected instructions='You are helpful', got %q", result.Instructions)
	}
	// System prompt should NOT be in input
	for _, input := range result.Input {
		if input.Role == "system" {
			t.Error("system prompt should be in instructions, not input")
		}
	}
}

func TestTranslateToOpenAIResponses_MultipleMessages(t *testing.T) {
	req := &types.AnthropicRequest{
		Model:     "gpt-5.2-codex",
		MaxTokens: 200,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"First message"`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"Response"}]`)},
			{Role: "user", Content: json.RawMessage(`"Follow-up"`)},
		},
	}

	result, err := TranslateToOpenAIResponses(req)
	if err != nil {
		t.Fatalf("TranslateToOpenAIResponses() error: %v", err)
	}

	if len(result.Input) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(result.Input))
	}
	for i, item := range result.Input {
		if item.Type != "message" {
			t.Errorf("input[%d]: expected type='message', got %q", i, item.Type)
		}
	}
}

func TestTranslateToOpenAIResponses_ToolUse(t *testing.T) {
	req := &types.AnthropicRequest{
		Model:     "gpt-5.2-codex",
		MaxTokens: 300,
		Messages: []types.Message{
			{
				Role: "assistant",
				Content: json.RawMessage(`[{
					"type": "tool_use",
					"id": "call_123",
					"name": "get_weather",
					"input": {"city": "SF"}
				}]`),
			},
		},
	}

	result, err := TranslateToOpenAIResponses(req)
	if err != nil {
		t.Fatalf("TranslateToOpenAIResponses() error: %v", err)
	}

	if len(result.Input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(result.Input))
	}
	if len(result.Input[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.Input[0].ToolCalls))
	}
	tc := result.Input[0].ToolCalls[0]
	if tc.ID != "call_123" {
		t.Errorf("expected tool call id='call_123', got %q", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("expected function name='get_weather', got %q", tc.Function.Name)
	}
}

func TestResponseInputTypeField(t *testing.T) {
	// Verify that type field is always "message"
	req := &types.AnthropicRequest{
		Model:     "gpt-5.2-codex",
		MaxTokens: 100,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Test"`)},
		},
	}

	result, err := TranslateToOpenAIResponses(req)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	for i, input := range result.Input {
		if input.Type != "message" {
			t.Errorf("input[%d]: type field missing or incorrect: got %q, want 'message'", i, input.Type)
		}
	}
}

func TestMaxOutputTokensVsMaxTokens(t *testing.T) {
	// Verify we use max_output_tokens, not max_tokens
	req := &types.AnthropicRequest{
		Model:     "gpt-5.2-codex",
		MaxTokens: 500,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Test"`)},
		},
	}

	result, err := TranslateToOpenAIResponses(req)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Marshal to JSON and verify field name
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	jsonStr := string(jsonBytes)
	if !strings.Contains(jsonStr, "max_output_tokens") {
		t.Error("JSON should contain 'max_output_tokens' field")
	}
	if strings.Contains(jsonStr, `"max_tokens"`) {
		t.Error("JSON should NOT contain 'max_tokens' field")
	}
}

func TestResponsesPayloadJSON(t *testing.T) {
	payload := &ResponsesPayload{
		Model: "gpt-5.2-codex",
		Input: []ResponseInput{
			{
				Type:    "message",
				Role:    "user",
				Content: "Hello",
			},
		},
		Instructions:    "Be helpful",
		MaxOutputTokens: 100,
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Failed to marshal ResponsesPayload: %v", err)
	}

	jsonStr := string(jsonBytes)

	// Verify key fields are present
	if !strings.Contains(jsonStr, `"input"`) {
		t.Error("JSON should contain 'input' field")
	}
	if !strings.Contains(jsonStr, `"instructions"`) {
		t.Error("JSON should contain 'instructions' field")
	}
	if !strings.Contains(jsonStr, `"max_output_tokens"`) {
		t.Error("JSON should contain 'max_output_tokens' field")
	}
	// Verify no 'messages' field (that's for chat completions)
	if strings.Contains(jsonStr, `"messages"`) {
		t.Error("JSON should NOT contain 'messages' field")
	}
}

func TestTranslateResponsesAPIToAnthropic_TextOutput(t *testing.T) {
	// Test the standard OpenAI Responses API format with output_text type
	resp := &ResponsesAPIResponse{
		ID:     "resp_abc123",
		Object: "response",
		Status: "completed",
		Output: []ResponseOutputItem{
			{
				Type: "message",
				Role: "assistant",
				Content: []interface{}{
					map[string]interface{}{
						"type": "output_text",
						"text": "Hello! How can I help you today?",
					},
				},
			},
		},
		Usage: &ResponsesUsage{
			InputTokens:  10,
			OutputTokens: 8,
		},
	}

	result := TranslateResponsesAPIToAnthropic(resp, "gpt-5.2-codex")

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Type != "text" {
		t.Errorf("expected content type='text', got %q", result.Content[0].Type)
	}
	if result.Content[0].Text != "Hello! How can I help you today?" {
		t.Errorf("unexpected text content: %q", result.Content[0].Text)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason='end_turn', got %q", result.StopReason)
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("expected input_tokens=10, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 8 {
		t.Errorf("expected output_tokens=8, got %d", result.Usage.OutputTokens)
	}
	// Message ID should be generated, not the raw response ID
	if !strings.HasPrefix(result.ID, "msg_") {
		t.Errorf("expected message ID to start with 'msg_', got %q", result.ID)
	}
}

func TestTranslateResponsesAPIToAnthropic_MultipleOutputText(t *testing.T) {
	// Test response with multiple output_text parts
	resp := &ResponsesAPIResponse{
		ID:     "resp_xyz789",
		Object: "response",
		Status: "completed",
		Output: []ResponseOutputItem{
			{
				Type: "message",
				Role: "assistant",
				Content: []interface{}{
					map[string]interface{}{
						"type": "output_text",
						"text": "First part. ",
					},
					map[string]interface{}{
						"type": "output_text",
						"text": "Second part.",
					},
				},
			},
		},
	}

	result := TranslateResponsesAPIToAnthropic(resp, "gpt-5.2-codex")

	if len(result.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(result.Content))
	}
	if result.Content[0].Text != "First part. " {
		t.Errorf("first text block mismatch: %q", result.Content[0].Text)
	}
	if result.Content[1].Text != "Second part." {
		t.Errorf("second text block mismatch: %q", result.Content[1].Text)
	}
}

func TestTranslateResponsesAPIToAnthropic_EmptyOutput(t *testing.T) {
	// Test response with empty output array
	resp := &ResponsesAPIResponse{
		ID:     "resp_empty",
		Object: "response",
		Status: "completed",
		Output: []ResponseOutputItem{},
	}

	result := TranslateResponsesAPIToAnthropic(resp, "gpt-5.2-codex")

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Content) != 0 {
		t.Errorf("expected empty content, got %d blocks", len(result.Content))
	}
}

func TestTranslateResponsesAPIToAnthropic_FunctionCall(t *testing.T) {
	// Test response with function call
	resp := &ResponsesAPIResponse{
		ID:     "resp_func",
		Object: "response",
		Status: "completed",
		Output: []ResponseOutputItem{
			{
				Type: "function_call",
				ID:   "call_abc123",
				Content: map[string]interface{}{
					"name":      "get_weather",
					"arguments": `{"city":"San Francisco"}`,
				},
			},
		},
	}

	result := TranslateResponsesAPIToAnthropic(resp, "gpt-5.2-codex")

	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Type != "tool_use" {
		t.Errorf("expected type='tool_use', got %q", result.Content[0].Type)
	}
	if result.Content[0].ID != "call_abc123" {
		t.Errorf("expected ID='call_abc123', got %q", result.Content[0].ID)
	}
	if result.Content[0].Name != "get_weather" {
		t.Errorf("expected Name='get_weather', got %q", result.Content[0].Name)
	}
}
