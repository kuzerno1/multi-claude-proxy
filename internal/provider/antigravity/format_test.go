package antigravity

import (
	"encoding/json"
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

func TestConvertRole(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user", "user"},
		{"assistant", "model"},
		{"other", "user"},
	}

	for _, tt := range tests {
		result := convertRole(tt.input)
		if result != tt.expected {
			t.Errorf("convertRole(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestSanitizeSchema(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:  "nil schema returns placeholder",
			input: nil,
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"reason": map[string]interface{}{
						"type":        "string",
						"description": "Reason for calling this tool",
					},
				},
				"required": []interface{}{"reason"},
			},
		},
		{
			name: "preserves allowed fields",
			input: map[string]interface{}{
				"type":        "object",
				"description": "A test schema",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name",
					},
				},
				"required": []interface{}{"name"},
			},
			expected: map[string]interface{}{
				"type":        "object",
				"description": "A test schema",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name",
					},
				},
				"required": []interface{}{"name"},
			},
		},
		{
			name: "removes unsupported fields",
			input: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"$schema":              "http://json-schema.org/draft-07/schema#",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			name: "converts const to enum",
			input: map[string]interface{}{
				"type":  "string",
				"const": "fixed_value",
			},
			expected: map[string]interface{}{
				"type": "string",
				"enum": []interface{}{"fixed_value"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeSchema(tt.input)

			// Check type
			if result["type"] != tt.expected["type"] {
				t.Errorf("type mismatch: got %v, want %v", result["type"], tt.expected["type"])
			}

			// Check description if present
			if tt.expected["description"] != nil {
				if result["description"] != tt.expected["description"] {
					t.Errorf("description mismatch: got %v, want %v", result["description"], tt.expected["description"])
				}
			}
		})
	}
}

func TestCleanSchema(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]interface{}
		check func(t *testing.T, result map[string]interface{})
	}{
		{
			name: "converts type to uppercase",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
				},
			},
			check: func(t *testing.T, result map[string]interface{}) {
				if result["type"] != "OBJECT" {
					t.Errorf("expected type OBJECT, got %v", result["type"])
				}
				if props, ok := result["properties"].(map[string]interface{}); ok {
					if name, ok := props["name"].(map[string]interface{}); ok {
						if name["type"] != "STRING" {
							t.Errorf("expected nested type STRING, got %v", name["type"])
						}
					}
				}
			},
		},
		{
			name: "removes unsupported keywords",
			input: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"default":              "test",
				"$schema":              "test",
			},
			check: func(t *testing.T, result map[string]interface{}) {
				if _, ok := result["additionalProperties"]; ok {
					t.Error("additionalProperties should be removed")
				}
				if _, ok := result["default"]; ok {
					t.Error("default should be removed")
				}
				if _, ok := result["$schema"]; ok {
					t.Error("$schema should be removed")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanSchema(tt.input)
			tt.check(t, result)
		})
	}
}

func TestConvertAnthropicToGoogle(t *testing.T) {
	req := &types.AnthropicRequest{
		Model:     "claude-sonnet-4-5-thinking",
		MaxTokens: 8192,
		Messages: []types.Message{
			{
				Role:    "user",
				Content: json.RawMessage(`"Hello, how are you?"`),
			},
		},
	}

	result := ConvertAnthropicToGoogle(req)

	// Check that contents were created
	contents, ok := result["contents"].([]interface{})
	if !ok || len(contents) == 0 {
		t.Error("expected contents array")
	}

	// Check generation config
	genConfig, ok := result["generationConfig"].(map[string]interface{})
	if !ok {
		t.Error("expected generationConfig")
	}

	if genConfig["maxOutputTokens"] != 8192 {
		t.Errorf("expected maxOutputTokens 8192, got %v", genConfig["maxOutputTokens"])
	}

	// Check thinking config for thinking model
	thinkingConfig, ok := genConfig["thinkingConfig"].(map[string]interface{})
	if !ok {
		t.Error("expected thinkingConfig for thinking model")
	}

	if thinkingConfig["include_thoughts"] != true {
		t.Error("expected include_thoughts to be true")
	}
}

func TestConvertGoogleToAnthropic(t *testing.T) {
	googleResp := map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"parts": []interface{}{
						map[string]interface{}{"text": "Hello!"},
					},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]interface{}{
			"promptTokenCount":     100,
			"candidatesTokenCount": 50,
		},
	}

	result := ConvertGoogleToAnthropic(googleResp, "claude-sonnet-4-5")

	if result.Role != "assistant" {
		t.Errorf("expected role assistant, got %s", result.Role)
	}

	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason end_turn, got %s", result.StopReason)
	}

	if len(result.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(result.Content))
	}

	if result.Content[0].Type != "text" {
		t.Errorf("expected text block, got %s", result.Content[0].Type)
	}

	if result.Content[0].Text != "Hello!" {
		t.Errorf("expected text 'Hello!', got '%s'", result.Content[0].Text)
	}

	if result.Usage.InputTokens != 100 {
		t.Errorf("expected input_tokens 100, got %d", result.Usage.InputTokens)
	}

	if result.Usage.OutputTokens != 50 {
		t.Errorf("expected output_tokens 50, got %d", result.Usage.OutputTokens)
	}
}

func TestConvertGoogleToAnthropic_ToolUse(t *testing.T) {
	googleResp := map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"parts": []interface{}{
						map[string]interface{}{
							"functionCall": map[string]interface{}{
								"name": "read_file",
								"args": map[string]interface{}{
									"path": "/test.txt",
								},
							},
						},
					},
				},
				"finishReason": "TOOL_USE",
			},
		},
	}

	result := ConvertGoogleToAnthropic(googleResp, "claude-sonnet-4-5")

	if result.StopReason != "tool_use" {
		t.Errorf("expected stop_reason tool_use, got %s", result.StopReason)
	}

	if len(result.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(result.Content))
	}

	if result.Content[0].Type != "tool_use" {
		t.Errorf("expected tool_use block, got %s", result.Content[0].Type)
	}

	if result.Content[0].Name != "read_file" {
		t.Errorf("expected name 'read_file', got '%s'", result.Content[0].Name)
	}
}

func TestConvertGoogleToAnthropic_Thinking(t *testing.T) {
	googleResp := map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"parts": []interface{}{
						map[string]interface{}{
							"text":             "Let me think about this...",
							"thought":          true,
							"thoughtSignature": "abc123def456abc123def456abc123def456abc123def456abc123",
						},
						map[string]interface{}{"text": "The answer is 42."},
					},
				},
				"finishReason": "STOP",
			},
		},
	}

	result := ConvertGoogleToAnthropic(googleResp, "claude-sonnet-4-5-thinking")

	if len(result.Content) != 2 {
		t.Errorf("expected 2 content blocks, got %d", len(result.Content))
	}

	if result.Content[0].Type != "thinking" {
		t.Errorf("expected thinking block, got %s", result.Content[0].Type)
	}

	if result.Content[0].Thinking != "Let me think about this..." {
		t.Errorf("expected thinking content, got '%s'", result.Content[0].Thinking)
	}

	if result.Content[1].Type != "text" {
		t.Errorf("expected text block, got %s", result.Content[1].Type)
	}

	if result.Content[1].Text != "The answer is 42." {
		t.Errorf("expected text content, got '%s'", result.Content[1].Text)
	}
}

func TestSignatureCache(t *testing.T) {
	cache := NewSignatureCache()

	// Test tool signature caching
	cache.CacheToolSignature("tool_123", "sig_abc")
	sig := cache.GetToolSignature("tool_123")
	if sig != "sig_abc" {
		t.Errorf("expected sig_abc, got %s", sig)
	}

	// Test missing signature
	sig = cache.GetToolSignature("nonexistent")
	if sig != "" {
		t.Errorf("expected empty string for missing key, got %s", sig)
	}

	// Test thinking signature caching (needs min length)
	longSig := "abc123def456abc123def456abc123def456abc123def456abc123"
	cache.CacheThinkingSignature(longSig, "claude")
	family := cache.GetSignatureFamily(longSig)
	if family != "claude" {
		t.Errorf("expected claude, got %s", family)
	}

	// Test too short signature (should not be cached)
	cache.CacheThinkingSignature("short", "gemini")
	family = cache.GetSignatureFamily("short")
	if family != "" {
		t.Errorf("expected empty string for short signature, got %s", family)
	}
}

// Tests for json.RawMessage content/system parsing (Node parity)
func TestConvertAnthropicToGoogle_JSONRawMessageContent(t *testing.T) {
	// Test string content (json.RawMessage wrapping a string)
	t.Run("string content", func(t *testing.T) {
		req := &types.AnthropicRequest{
			Model:     "claude-sonnet-4-5",
			MaxTokens: 1024,
			Messages: []types.Message{
				{
					Role:    "user",
					Content: json.RawMessage(`"Hello, world!"`),
				},
			},
		}

		result := ConvertAnthropicToGoogle(req)
		contents := result["contents"].([]interface{})
		if len(contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(contents))
		}

		content := contents[0].(map[string]interface{})
		parts := content["parts"].([]interface{})
		if len(parts) != 1 {
			t.Fatalf("expected 1 part, got %d", len(parts))
		}

		part := parts[0].(map[string]interface{})
		if part["text"] != "Hello, world!" {
			t.Errorf("expected 'Hello, world!', got '%v'", part["text"])
		}
	})

	// Test array content (json.RawMessage wrapping an array)
	t.Run("array content with text block", func(t *testing.T) {
		req := &types.AnthropicRequest{
			Model:     "claude-sonnet-4-5",
			MaxTokens: 1024,
			Messages: []types.Message{
				{
					Role:    "user",
					Content: json.RawMessage(`[{"type": "text", "text": "Hello from array!"}]`),
				},
			},
		}

		result := ConvertAnthropicToGoogle(req)
		contents := result["contents"].([]interface{})
		content := contents[0].(map[string]interface{})
		parts := content["parts"].([]interface{})

		if len(parts) != 1 {
			t.Fatalf("expected 1 part, got %d", len(parts))
		}

		part := parts[0].(map[string]interface{})
		if part["text"] != "Hello from array!" {
			t.Errorf("expected 'Hello from array!', got '%v'", part["text"])
		}
	})
}

func TestConvertAnthropicToGoogle_JSONRawMessageSystem(t *testing.T) {
	// Test string system prompt
	t.Run("string system", func(t *testing.T) {
		req := &types.AnthropicRequest{
			Model:     "claude-sonnet-4-5",
			MaxTokens: 1024,
			System:    json.RawMessage(`"You are a helpful assistant."`),
			Messages: []types.Message{
				{
					Role:    "user",
					Content: json.RawMessage(`"Hello"`),
				},
			},
		}

		result := ConvertAnthropicToGoogle(req)
		sysInstr, ok := result["systemInstruction"].(map[string]interface{})
		if !ok {
			t.Fatal("expected systemInstruction")
		}

		parts := sysInstr["parts"].([]interface{})
		if len(parts) != 1 {
			t.Fatalf("expected 1 part, got %d", len(parts))
		}

		part := parts[0].(map[string]interface{})
		if part["text"] != "You are a helpful assistant." {
			t.Errorf("expected system text, got '%v'", part["text"])
		}
	})

	// Test array system prompt
	t.Run("array system", func(t *testing.T) {
		req := &types.AnthropicRequest{
			Model:     "claude-sonnet-4-5",
			MaxTokens: 1024,
			System:    json.RawMessage(`[{"type": "text", "text": "You are helpful."}, {"type": "text", "text": "Be concise."}]`),
			Messages: []types.Message{
				{
					Role:    "user",
					Content: json.RawMessage(`"Hello"`),
				},
			},
		}

		result := ConvertAnthropicToGoogle(req)
		sysInstr := result["systemInstruction"].(map[string]interface{})
		parts := sysInstr["parts"].([]interface{})

		if len(parts) != 2 {
			t.Fatalf("expected 2 parts, got %d", len(parts))
		}

		part0 := parts[0].(map[string]interface{})
		if part0["text"] != "You are helpful." {
			t.Errorf("expected first system text, got '%v'", part0["text"])
		}

		part1 := parts[1].(map[string]interface{})
		if part1["text"] != "Be concise." {
			t.Errorf("expected second system text, got '%v'", part1["text"])
		}
	})
}

// Test tool_result uses tool_use_id for functionResponse.name (Node parity)
func TestConvertContentToParts_ToolResultUsesToolUseID(t *testing.T) {
	content := json.RawMessage(`[{
		"type": "tool_result",
		"tool_use_id": "toolu_abc123",
		"content": "Tool execution result"
	}]`)

	parts := convertContentToParts(content, true, false) // isClaudeModel=true

	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	part := parts[0].(map[string]interface{})
	funcResp, ok := part["functionResponse"].(map[string]interface{})
	if !ok {
		t.Fatal("expected functionResponse")
	}

	// Check that name uses tool_use_id (Node parity)
	if funcResp["name"] != "toolu_abc123" {
		t.Errorf("expected functionResponse.name = 'toolu_abc123', got '%v'", funcResp["name"])
	}

	// Check that id also uses tool_use_id for Claude models
	if funcResp["id"] != "toolu_abc123" {
		t.Errorf("expected functionResponse.id = 'toolu_abc123', got '%v'", funcResp["id"])
	}

	// Check response content
	resp := funcResp["response"].(map[string]interface{})
	if resp["result"] != "Tool execution result" {
		t.Errorf("expected response.result, got '%v'", resp["result"])
	}
}

func TestConvertContentToParts_ToolResultWithMissingID(t *testing.T) {
	content := json.RawMessage(`[{
		"type": "tool_result",
		"content": "Tool execution result"
	}]`)

	parts := convertContentToParts(content, false, false)

	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	part := parts[0].(map[string]interface{})
	funcResp := part["functionResponse"].(map[string]interface{})

	// Should fall back to "unknown" when tool_use_id is missing
	if funcResp["name"] != "unknown" {
		t.Errorf("expected functionResponse.name = 'unknown', got '%v'", funcResp["name"])
	}
}

// Test document blocks (Node parity)
func TestConvertContentToParts_DocumentBlock(t *testing.T) {
	content := json.RawMessage(`[{
		"type": "document",
		"source": {
			"type": "base64",
			"media_type": "application/pdf",
			"data": "JVBERi0xLjQK..."
		}
	}]`)

	parts := convertContentToParts(content, false, false)

	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	part := parts[0].(map[string]interface{})
	inlineData, ok := part["inlineData"].(map[string]interface{})
	if !ok {
		t.Fatal("expected inlineData for document")
	}

	if inlineData["mimeType"] != "application/pdf" {
		t.Errorf("expected mimeType 'application/pdf', got '%v'", inlineData["mimeType"])
	}

	if inlineData["data"] != "JVBERi0xLjQK..." {
		t.Errorf("expected data, got '%v'", inlineData["data"])
	}
}

// Test tool_result with image extraction (Node parity)
func TestProcessToolResultContentTyped_WithImages(t *testing.T) {
	content := json.RawMessage(`[
		{"type": "text", "text": "Here is the image:"},
		{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}}
	]`)

	result, imageParts := processToolResultContentTyped(content)

	// Check text result
	if result["result"] != "Here is the image:" {
		t.Errorf("expected text result, got '%v'", result["result"])
	}

	// Check image was extracted
	if len(imageParts) != 1 {
		t.Fatalf("expected 1 image part, got %d", len(imageParts))
	}

	imgPart := imageParts[0].(map[string]interface{})
	inlineData := imgPart["inlineData"].(map[string]interface{})
	if inlineData["mimeType"] != "image/png" {
		t.Errorf("expected mimeType 'image/png', got '%v'", inlineData["mimeType"])
	}
}

func TestProcessToolResultContentTyped_ImageOnly(t *testing.T) {
	content := json.RawMessage(`[
		{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}}
	]`)

	result, imageParts := processToolResultContentTyped(content)

	// When only images, result should say "Image attached"
	if result["result"] != "Image attached" {
		t.Errorf("expected 'Image attached', got '%v'", result["result"])
	}

	if len(imageParts) != 1 {
		t.Fatalf("expected 1 image part, got %d", len(imageParts))
	}
}

// ============================================================================
// Parity Tests (Node.js parity)
// ============================================================================

// Test: Assistant processing runs even when model is non-thinking (parity fix #1)
func TestAssistantProcessing_NonThinkingModel(t *testing.T) {
	// Create a non-thinking Claude model request with assistant message
	// that has content needing reordering
	req := &types.AnthropicRequest{
		Model:     "claude-sonnet-4-5", // Non-thinking model
		MaxTokens: 1024,
		Messages: []types.Message{
			{
				Role:    "user",
				Content: json.RawMessage(`"Hello"`),
			},
			{
				Role:    "assistant",
				Content: json.RawMessage(`[{"type": "tool_use", "id": "tool_1", "name": "test", "input": {}}, {"type": "text", "text": "Hello"}]`),
			},
			{
				Role:    "user",
				Content: json.RawMessage(`[{"type": "tool_result", "tool_use_id": "tool_1", "content": "result"}]`),
			},
		},
	}

	result := ConvertAnthropicToGoogle(req)
	contents := result["contents"].([]interface{})

	// Check that assistant message content was reordered (text before tool_use)
	if len(contents) < 2 {
		t.Fatalf("expected at least 2 contents, got %d", len(contents))
	}

	assistantContent := contents[1].(map[string]interface{})
	parts := assistantContent["parts"].([]interface{})

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	// First part should be text (reordering: text before tool_use)
	firstPart := parts[0].(map[string]interface{})
	if _, hasText := firstPart["text"]; !hasText {
		t.Error("expected first part to be text (reordering should have happened)")
	}

	// Second part should be functionCall
	secondPart := parts[1].(map[string]interface{})
	if _, hasFunctionCall := secondPart["functionCall"]; !hasFunctionCall {
		t.Error("expected second part to be functionCall")
	}
}

// Test: Claude filterUnsignedThinkingBlocks filters unsigned thinking (parity fix #2)
func TestFilterUnsignedThinkingBlocks(t *testing.T) {
	contents := []interface{}{
		map[string]interface{}{
			"role": "model",
			"parts": []interface{}{
				map[string]interface{}{"text": "Hello", "thought": true}, // No signature - should be dropped
				map[string]interface{}{"text": "World"},                  // Not thinking - should be kept
			},
		},
	}

	result := FilterUnsignedThinkingBlocks(contents)

	resultContent := result[0].(map[string]interface{})
	resultParts := resultContent["parts"].([]interface{})

	if len(resultParts) != 1 {
		t.Fatalf("expected 1 part after filtering, got %d", len(resultParts))
	}

	part := resultParts[0].(map[string]interface{})
	if part["text"] != "World" {
		t.Errorf("expected 'World', got '%v'", part["text"])
	}
}

// Test: filterUnsignedThinkingBlocks keeps valid signed thinking
func TestFilterUnsignedThinkingBlocks_KeepsSignedThinking(t *testing.T) {
	longSig := "abc123def456abc123def456abc123def456abc123def456abc123" // 54 chars
	contents := []interface{}{
		map[string]interface{}{
			"role": "model",
			"parts": []interface{}{
				map[string]interface{}{
					"text":             "Thinking...",
					"thought":          true,
					"thoughtSignature": longSig,
				},
				map[string]interface{}{"text": "Result"},
			},
		},
	}

	result := FilterUnsignedThinkingBlocks(contents)

	resultContent := result[0].(map[string]interface{})
	resultParts := resultContent["parts"].([]interface{})

	if len(resultParts) != 2 {
		t.Fatalf("expected 2 parts (signed thinking kept), got %d", len(resultParts))
	}
}

// Test: Tool-loop recovery uses fmt.Sprintf for multi-digit counts (parity fix #3)
func TestCloseToolLoopForThinking_MultiDigitCount(t *testing.T) {
	// Create a scenario with 12 tool result messages
	messages := []types.Message{
		{Role: "user", Content: json.RawMessage(`"Start"`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type": "tool_use", "id": "t1", "name": "test", "input": {}}]`)},
	}

	// Add 12 tool result messages
	for i := 0; i < 12; i++ {
		messages = append(messages, types.Message{
			Role:    "user",
			Content: json.RawMessage(`[{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}]`),
		})
	}

	result := closeToolLoopForThinking(messages, "gemini")

	// Should have synthetic assistant + user messages at the end
	if len(result) != len(messages)+2 {
		t.Fatalf("expected %d messages, got %d", len(messages)+2, len(result))
	}

	// Check synthetic assistant message
	syntheticAssistant := result[len(result)-2]
	var blocks []types.ContentBlock
	json.Unmarshal(syntheticAssistant.Content, &blocks)

	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Fatal("expected synthetic text block")
	}

	// Should correctly format multi-digit number (not use rune math)
	expectedText := "[12 tool executions completed.]"
	if blocks[0].Text != expectedText {
		t.Errorf("expected '%s', got '%s'", expectedText, blocks[0].Text)
	}
}

// Test: redacted_thinking.data is preserved (parity fix #4)
func TestReorderAssistantContent_PreservesRedactedThinkingData(t *testing.T) {
	blocks := []types.ContentBlock{
		{
			Type: "redacted_thinking",
			Data: "base64encodeddata==",
		},
		{
			Type: "text",
			Text: "Hello",
		},
	}

	result := reorderAssistantContent(blocks)

	if len(result) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result))
	}

	// First should be redacted_thinking
	if result[0].Type != "redacted_thinking" {
		t.Errorf("expected redacted_thinking first, got %s", result[0].Type)
	}

	// Data should be preserved
	if result[0].Data != "base64encodeddata==" {
		t.Errorf("expected Data to be preserved, got '%s'", result[0].Data)
	}
}

// Test: restoreThinkingSignatures preserves redacted_thinking blocks
func TestRestoreThinkingSignatures_PreservesRedactedThinking(t *testing.T) {
	blocks := []types.ContentBlock{
		{
			Type: "redacted_thinking",
			Data: "somedata==",
		},
		{
			Type:      "thinking",
			Thinking:  "Unsigned thinking",
			Signature: "", // No signature - should be dropped
		},
		{
			Type: "text",
			Text: "Hello",
		},
	}

	result := restoreThinkingSignatures(blocks)

	// Should have 2 blocks: redacted_thinking (kept) and text
	// Unsigned thinking is dropped
	if len(result) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result))
	}

	if result[0].Type != "redacted_thinking" {
		t.Errorf("expected redacted_thinking, got %s", result[0].Type)
	}

	if result[0].Data != "somedata==" {
		t.Errorf("expected Data preserved, got '%s'", result[0].Data)
	}

	if result[1].Type != "text" {
		t.Errorf("expected text, got %s", result[1].Type)
	}
}

// Test: Empty/whitespace text filtering (parity fix #5)
func TestReorderAssistantContent_FiltersWhitespaceText(t *testing.T) {
	blocks := []types.ContentBlock{
		{Type: "text", Text: "   "},   // Whitespace only - should be dropped
		{Type: "text", Text: "\t\n"},  // Whitespace only - should be dropped
		{Type: "text", Text: ""},      // Empty - should be dropped
		{Type: "text", Text: "Hello"}, // Has content - should be kept
		{Type: "text", Text: " Hi "},  // Has content after trim - should be kept
	}

	result := reorderAssistantContent(blocks)

	if len(result) != 2 {
		t.Fatalf("expected 2 blocks after filtering whitespace, got %d", len(result))
	}

	if result[0].Text != "Hello" {
		t.Errorf("expected 'Hello', got '%s'", result[0].Text)
	}

	if result[1].Text != " Hi " {
		t.Errorf("expected ' Hi ', got '%s'", result[1].Text)
	}
}

// Test: convertBlockToPart filters whitespace-only text
func TestConvertBlockToPart_FiltersWhitespaceText(t *testing.T) {
	// Whitespace-only text should return nil
	block := types.ContentBlock{Type: "text", Text: "   "}
	result := convertBlockToPart(block, false, false)
	if result != nil {
		t.Error("expected nil for whitespace-only text block")
	}

	// Non-whitespace text should return a part
	block = types.ContentBlock{Type: "text", Text: "Hello"}
	result = convertBlockToPart(block, false, false)
	if result == nil {
		t.Error("expected non-nil for text block with content")
	}
}

// Test: Tool-loop counting counts MESSAGES not BLOCKS (parity fix #3)
func TestAnalyzeConversationState_CountsMessages(t *testing.T) {
	// Create a message with multiple tool_result blocks in one message
	messages := []types.Message{
		{Role: "user", Content: json.RawMessage(`"Start"`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type": "tool_use", "id": "t1", "name": "test", "input": {}}]`)},
		{
			Role:    "user",
			Content: json.RawMessage(`[{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}, {"type": "tool_result", "tool_use_id": "t2", "content": "ok2"}]`),
		},
	}

	state := analyzeConversationState(messages)

	// Should count 1 message, not 2 blocks
	if state.ToolResultCount != 1 {
		t.Errorf("expected ToolResultCount=1 (messages), got %d", state.ToolResultCount)
	}
}
