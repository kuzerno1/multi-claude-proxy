package copilot

import (
	"encoding/json"
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

func TestTranslateToOpenAI_BasicMessage(t *testing.T) {
	req := &types.AnthropicRequest{
		Model:     "gpt-4",
		MaxTokens: 1000,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Hello, world!"`)},
		},
	}

	payload, err := TranslateToOpenAI(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if payload.Model != "gpt-4" {
		t.Errorf("expected model gpt-4, got %s", payload.Model)
	}

	if payload.MaxTokens != 1000 {
		t.Errorf("expected max tokens 1000, got %d", payload.MaxTokens)
	}

	if len(payload.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(payload.Messages))
	}

	if payload.Messages[0].Role != "user" {
		t.Errorf("expected role user, got %s", payload.Messages[0].Role)
	}
}

func TestTranslateToOpenAI_WithSystemPrompt(t *testing.T) {
	req := &types.AnthropicRequest{
		Model:     "gpt-4",
		MaxTokens: 1000,
		System:    json.RawMessage(`"You are a helpful assistant."`),
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
		},
	}

	payload, err := TranslateToOpenAI(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(payload.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(payload.Messages))
	}

	if payload.Messages[0].Role != "system" {
		t.Errorf("expected first message role system, got %s", payload.Messages[0].Role)
	}

	if payload.Messages[1].Role != "user" {
		t.Errorf("expected second message role user, got %s", payload.Messages[1].Role)
	}
}

func TestTranslateToOpenAI_WithTools(t *testing.T) {
	req := &types.AnthropicRequest{
		Model:     "gpt-4",
		MaxTokens: 1000,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"What's the weather?"`)},
		},
		Tools: []types.Tool{
			{
				Name:        "get_weather",
				Description: "Get the current weather",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
		},
	}

	payload, err := TranslateToOpenAI(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(payload.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(payload.Tools))
	}

	if payload.Tools[0].Function.Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", payload.Tools[0].Function.Name)
	}
}

func TestTranslateToAnthropic_BasicResponse(t *testing.T) {
	resp := &ChatCompletionResponse{
		ID: "chatcmpl-123",
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    "assistant",
					Content: "Hello! How can I help you?",
				},
				FinishReason: "stop",
			},
		},
		Usage: &Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}

	anthropicResp := TranslateToAnthropic(resp, "gpt-4")

	if anthropicResp.ID != "chatcmpl-123" {
		t.Errorf("expected ID chatcmpl-123, got %s", anthropicResp.ID)
	}

	if anthropicResp.Role != "assistant" {
		t.Errorf("expected role assistant, got %s", anthropicResp.Role)
	}

	if len(anthropicResp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(anthropicResp.Content))
	}

	if anthropicResp.Content[0].Type != "text" {
		t.Errorf("expected content type text, got %s", anthropicResp.Content[0].Type)
	}

	if anthropicResp.StopReason != "end_turn" {
		t.Errorf("expected stop reason end_turn, got %s", anthropicResp.StopReason)
	}
}

func TestTranslateToAnthropic_WithToolCalls(t *testing.T) {
	resp := &ChatCompletionResponse{
		ID: "chatcmpl-456",
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{
							ID:   "call_123",
							Type: "function",
							Function: FunctionCall{
								Name:      "get_weather",
								Arguments: `{"location":"NYC"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}

	anthropicResp := TranslateToAnthropic(resp, "gpt-4")

	if len(anthropicResp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(anthropicResp.Content))
	}

	if anthropicResp.Content[0].Type != "tool_use" {
		t.Errorf("expected content type tool_use, got %s", anthropicResp.Content[0].Type)
	}

	if anthropicResp.Content[0].ID != "call_123" {
		t.Errorf("expected tool call ID call_123, got %s", anthropicResp.Content[0].ID)
	}

	if anthropicResp.Content[0].Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", anthropicResp.Content[0].Name)
	}

	if anthropicResp.StopReason != "tool_use" {
		t.Errorf("expected stop reason tool_use, got %s", anthropicResp.StopReason)
	}
}

func TestTranslateStopReason(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"content_filter", "end_turn"},
		{"unknown", "end_turn"},
	}

	for _, test := range tests {
		result := translateStopReason(test.input)
		if result != test.expected {
			t.Errorf("translateStopReason(%q) = %q, want %q", test.input, result, test.expected)
		}
	}
}

func TestTranslateToolChoice(t *testing.T) {
	tests := []struct {
		name     string
		input    *types.ToolChoice
		expected interface{}
	}{
		{"nil", nil, nil},
		{"auto", &types.ToolChoice{Type: "auto"}, "auto"},
		{"none", &types.ToolChoice{Type: "none"}, "none"},
		{"any", &types.ToolChoice{Type: "any"}, "required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := translateToolChoice(test.input)
			if test.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			if result != test.expected {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
		})
	}
}

func TestParseBase64Image_Valid(t *testing.T) {
	// Small valid PNG header in base64
	dataURL := "data:image/png;base64,iVBORw0KGgo="

	mediaType, data, err := ParseBase64Image(dataURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mediaType != "image/png" {
		t.Errorf("expected media type image/png, got %s", mediaType)
	}

	if data != "iVBORw0KGgo=" {
		t.Errorf("expected data iVBORw0KGgo=, got %s", data)
	}
}

func TestParseBase64Image_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		dataURL string
	}{
		{"not data URL", "https://example.com/image.png"},
		{"no comma", "data:image/png;base64"},
		{"not base64", "data:image/png;utf8,hello"},
		{"invalid base64", "data:image/png;base64,!!!invalid!!!"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := ParseBase64Image(test.dataURL)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestGenerateMessageID(t *testing.T) {
	id1 := GenerateMessageID()
	id2 := GenerateMessageID()

	if id1 == id2 {
		t.Error("expected unique message IDs")
	}

	if len(id1) != 28 { // "msg_" + 24 chars
		t.Errorf("expected ID length 28, got %d", len(id1))
	}

	if id1[:4] != "msg_" {
		t.Errorf("expected ID to start with msg_, got %s", id1[:4])
	}
}

func TestIsBase64Image(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"data:image/png;base64,abc", true},
		{"data:image/jpeg;base64,xyz", true},
		{"https://example.com/img.png", false},
		{"just some text", false},
	}

	for _, test := range tests {
		result := IsBase64Image(test.input)
		if result != test.expected {
			t.Errorf("IsBase64Image(%q) = %v, want %v", test.input, result, test.expected)
		}
	}
}
