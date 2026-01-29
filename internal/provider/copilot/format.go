package copilot

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// TranslateToOpenAI converts an Anthropic request to OpenAI format.
func TranslateToOpenAI(req *types.AnthropicRequest) (*ChatCompletionsPayload, error) {
	payload := &ChatCompletionsPayload{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Stop:        req.StopSequences,
	}

	// Convert system prompt to system message
	if len(req.System) > 0 {
		systemText, err := extractSystemText(req.System)
		if err != nil {
			return nil, fmt.Errorf("failed to parse system prompt: %w", err)
		}
		if systemText != "" {
			payload.Messages = append(payload.Messages, Message{
				Role:    "system",
				Content: systemText,
			})
		}
	}

	// Convert messages
	for _, msg := range req.Messages {
		openAIMsg, err := translateMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to translate message: %w", err)
		}
		payload.Messages = append(payload.Messages, openAIMsg...)
	}

	// Convert tools
	for _, tool := range req.Tools {
		payload.Tools = append(payload.Tools, Tool{
			Type: "function",
			Function: FunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}

	// Convert tool choice
	if req.ToolChoice != nil {
		payload.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	return payload, nil
}

// TranslateToOpenAIResponses converts an Anthropic request to OpenAI Responses API format.
// This format is used for models that support the /responses endpoint.
func TranslateToOpenAIResponses(req *types.AnthropicRequest) (*ResponsesPayload, error) {
	payload := &ResponsesPayload{
		Model:           req.Model,
		MaxOutputTokens: req.MaxTokens, // Note: Different field name than chat completions
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		Stream:          req.Stream,
		Stop:            req.StopSequences,
	}

	// Extract system prompt for Instructions field (not a message)
	if len(req.System) > 0 {
		systemText, err := extractSystemText(req.System)
		if err != nil {
			return nil, fmt.Errorf("failed to parse system prompt: %w", err)
		}
		if systemText != "" {
			payload.Instructions = systemText
		}
	}

	// Convert messages to input items
	for _, msg := range req.Messages {
		inputItems, err := translateMessageToInput(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to translate message: %w", err)
		}
		payload.Input = append(payload.Input, inputItems...)
	}

	// Convert tools
	for _, tool := range req.Tools {
		payload.Tools = append(payload.Tools, Tool{
			Type: "function",
			Function: FunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}

	// Convert tool choice
	if req.ToolChoice != nil {
		payload.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	return payload, nil
}

// translateMessageToInput converts an Anthropic message to Responses API input items.
func translateMessageToInput(msg types.Message) ([]ResponseInput, error) {
	blocks, err := types.ParseMessageContent(msg.Content)
	if err != nil {
		return nil, err
	}

	switch msg.Role {
	case "user":
		return translateUserMessageToInput(blocks)
	case "assistant":
		return translateAssistantMessageToInput(blocks)
	default:
		return nil, fmt.Errorf("unknown role: %s", msg.Role)
	}
}

// translateUserMessageToInput converts user message to Responses API input items.
func translateUserMessageToInput(blocks []types.ContentBlock) ([]ResponseInput, error) {
	var inputs []ResponseInput
	var contentParts []interface{}
	var toolResults []ResponseInput

	for _, block := range blocks {
		switch block.Type {
		case "text":
			contentParts = append(contentParts, map[string]interface{}{
				"type": "text",
				"text": block.Text,
			})
		case "image":
			if block.Source != nil {
				imgPart := translateImage(block.Source)
				if imgPart != nil {
					contentParts = append(contentParts, imgPart)
				}
			}
		case "tool_result":
			// Tool results become separate tool input items
			toolResults = append(toolResults, ResponseInput{
				Type:       "message", // Required field for Responses API
				Role:       "tool",
				Content:    extractToolResultContent(block),
				ToolCallID: block.ToolUseID,
			})
		}
	}

	// Add user message if there's content
	if len(contentParts) > 0 {
		var content interface{}
		if len(contentParts) == 1 {
			// Single text block - use string
			if textPart, ok := contentParts[0].(map[string]interface{}); ok {
				if textPart["type"] == "text" {
					content = textPart["text"]
				} else {
					content = contentParts
				}
			} else {
				content = contentParts
			}
		} else {
			content = contentParts
		}
		inputs = append(inputs, ResponseInput{
			Type:    "message", // Required field for Responses API
			Role:    "user",
			Content: content,
		})
	}

	// Add tool results
	inputs = append(inputs, toolResults...)

	return inputs, nil
}

// translateAssistantMessageToInput converts assistant message to Responses API input items.
func translateAssistantMessageToInput(blocks []types.ContentBlock) ([]ResponseInput, error) {
	input := ResponseInput{
		Type: "message", // Required field for Responses API
		Role: "assistant",
	}

	var textParts []string
	var toolCalls []ToolCall

	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking":
			// OpenAI doesn't have a thinking block equivalent - skip it
		case "tool_use":
			inputJSON, err := json.Marshal(block.Input)
			if err != nil {
				inputJSON = []byte("{}")
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(inputJSON),
				},
			})
		}
	}

	if len(textParts) > 0 {
		input.Content = strings.Join(textParts, "")
	}
	if len(toolCalls) > 0 {
		input.ToolCalls = toolCalls
	}

	return []ResponseInput{input}, nil
}

// extractSystemText extracts text from the system prompt.
func extractSystemText(system json.RawMessage) (string, error) {
	blocks, err := types.ParseSystemPrompt(system)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, block := range blocks {
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// translateMessage converts an Anthropic message to OpenAI format.
// May return multiple messages (e.g., tool results become separate messages).
func translateMessage(msg types.Message) ([]Message, error) {
	blocks, err := types.ParseMessageContent(msg.Content)
	if err != nil {
		return nil, err
	}

	switch msg.Role {
	case "user":
		return translateUserMessage(blocks)
	case "assistant":
		return translateAssistantMessage(blocks)
	default:
		return nil, fmt.Errorf("unknown role: %s", msg.Role)
	}
}

// translateUserMessage converts a user message to OpenAI format.
func translateUserMessage(blocks []types.ContentBlock) ([]Message, error) {
	var messages []Message
	var contentParts []interface{}
	var toolResults []Message

	for _, block := range blocks {
		switch block.Type {
		case "text":
			contentParts = append(contentParts, map[string]interface{}{
				"type": "text",
				"text": block.Text,
			})
		case "image":
			if block.Source != nil {
				imgPart := translateImage(block.Source)
				if imgPart != nil {
					contentParts = append(contentParts, imgPart)
				}
			}
		case "tool_result":
			// Tool results become separate tool messages
			toolResults = append(toolResults, Message{
				Role:       "tool",
				Content:    extractToolResultContent(block),
				ToolCallID: block.ToolUseID,
			})
		}
	}

	// Add the user message if there's content
	if len(contentParts) > 0 {
		var content interface{}
		if len(contentParts) == 1 {
			// Single text block - use string
			if textPart, ok := contentParts[0].(map[string]interface{}); ok {
				if textPart["type"] == "text" {
					content = textPart["text"]
				} else {
					content = contentParts
				}
			} else {
				content = contentParts
			}
		} else {
			content = contentParts
		}
		messages = append(messages, Message{
			Role:    "user",
			Content: content,
		})
	}

	// Add tool results as separate messages
	messages = append(messages, toolResults...)

	return messages, nil
}

// translateAssistantMessage converts an assistant message to OpenAI format.
func translateAssistantMessage(blocks []types.ContentBlock) ([]Message, error) {
	msg := Message{
		Role: "assistant",
	}

	var textParts []string
	var toolCalls []ToolCall

	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking":
			// OpenAI doesn't have a thinking block equivalent
			// We could include it as text or skip it
			// For now, skip it to match expected behavior
		case "tool_use":
			inputJSON, err := json.Marshal(block.Input)
			if err != nil {
				// Use empty object if input can't be marshaled
				inputJSON = []byte("{}")
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(inputJSON),
				},
			})
		}
	}

	if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "")
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	return []Message{msg}, nil
}

// translateImage converts an Anthropic image source to OpenAI format.
func translateImage(source *types.ImageSource) map[string]interface{} {
	if source == nil {
		return nil
	}

	var url string
	switch source.Type {
	case "base64":
		url = fmt.Sprintf("data:%s;base64,%s", source.MediaType, source.Data)
	case "url":
		url = source.URL
	default:
		return nil
	}

	return map[string]interface{}{
		"type": "image_url",
		"image_url": map[string]interface{}{
			"url": url,
		},
	}
}

// extractToolResultContent extracts the content from a tool result block.
func extractToolResultContent(block types.ContentBlock) string {
	if len(block.Content) == 0 {
		return ""
	}

	// Try parsing as string
	var str string
	if err := json.Unmarshal(block.Content, &str); err == nil {
		return str
	}

	// Try parsing as array of content blocks
	var contentBlocks []types.ContentBlock
	if err := json.Unmarshal(block.Content, &contentBlocks); err == nil {
		var parts []string
		for _, cb := range contentBlocks {
			if cb.Type == "text" {
				parts = append(parts, cb.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	// Fallback: return raw JSON
	return string(block.Content)
}

// translateToolChoice converts Anthropic tool_choice to OpenAI format.
func translateToolChoice(tc *types.ToolChoice) interface{} {
	if tc == nil {
		return nil
	}

	switch tc.Type {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		return ToolChoiceFunction{
			Type: "function",
			Function: ToolChoiceFunctionID{
				Name: tc.Name,
			},
		}
	default:
		return "auto"
	}
}

// TranslateToAnthropic converts an OpenAI response to Anthropic format.
func TranslateToAnthropic(resp *ChatCompletionResponse, model string) *types.AnthropicResponse {
	if len(resp.Choices) == 0 {
		return &types.AnthropicResponse{
			ID:      resp.ID,
			Type:    "message",
			Role:    "assistant",
			Content: []types.ContentBlock{},
			Model:   model,
			Usage:   types.Usage{},
		}
	}

	choice := resp.Choices[0]
	content := translateResponseContent(choice.Message)

	var usage types.Usage
	if resp.Usage != nil {
		usage = types.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
		if resp.Usage.PromptTokensDetails != nil {
			usage.CacheReadInputTokens = resp.Usage.PromptTokensDetails.CachedTokens
		}
	}

	return &types.AnthropicResponse{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      model,
		StopReason: translateStopReason(choice.FinishReason),
		Usage:      usage,
	}
}

// translateResponseContent converts OpenAI message content to Anthropic content blocks.
func translateResponseContent(msg Message) []types.ContentBlock {
	var blocks []types.ContentBlock

	// Handle text content
	if msg.Content != nil {
		switch content := msg.Content.(type) {
		case string:
			if content != "" {
				blocks = append(blocks, types.ContentBlock{
					Type: "text",
					Text: content,
				})
			}
		}
	}

	// Handle tool calls
	for _, tc := range msg.ToolCalls {
		var input map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
			// Log warning but continue with empty input rather than failing
			input = make(map[string]interface{})
		}

		blocks = append(blocks, types.ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	return blocks
}

// translateStopReason converts OpenAI finish_reason to Anthropic stop_reason.
func translateStopReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// TranslateResponsesAPIToAnthropic converts a Responses API response to Anthropic format.
func TranslateResponsesAPIToAnthropic(resp *ResponsesAPIResponse, model string) *types.AnthropicResponse {
	var content []types.ContentBlock

	// Process output items - the primary source of content
	// Note: OutputText is a computed property in OpenAI SDK, not sent in JSON response
	if len(resp.Output) > 0 {
		for _, item := range resp.Output {
			blocks := translateResponsesOutputItem(item)
			content = append(content, blocks...)
		}
	}

	// Build usage info
	var usage types.Usage
	if resp.Usage != nil {
		usage = types.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		}
	}

	// Determine stop reason from status
	stopReason := translateResponsesStatus(resp.Status)

	// Generate a proper message ID (the API's ID may be a token/signature)
	messageID := GenerateMessageID()

	return &types.AnthropicResponse{
		ID:         messageID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      model,
		StopReason: stopReason,
		Usage:      usage,
	}
}

// translateResponsesOutputItem converts a Responses API output item to Anthropic content blocks.
func translateResponsesOutputItem(item ResponseOutputItem) []types.ContentBlock {
	var blocks []types.ContentBlock

	switch item.Type {
	case "message":
		// Handle message content
		if item.Content != nil {
			switch content := item.Content.(type) {
			case string:
				// Direct string content
				if content != "" {
					blocks = append(blocks, types.ContentBlock{
						Type: "text",
						Text: content,
					})
				}
			case []interface{}:
				// Array of content parts - this is the standard format
				for _, part := range content {
					if partMap, ok := part.(map[string]interface{}); ok {
						blocks = append(blocks, extractContentFromPart(partMap)...)
					}
				}
			case map[string]interface{}:
				// Single content object
				blocks = append(blocks, extractContentFromPart(content)...)
			}
		}
	case "function_call":
		// Handle function/tool calls
		if item.ID != "" {
			var input map[string]interface{}
			var name string

			if item.Content != nil {
				if contentMap, ok := item.Content.(map[string]interface{}); ok {
					if n, ok := contentMap["name"].(string); ok {
						name = n
					}
					if args, ok := contentMap["arguments"].(string); ok {
						// Try to parse arguments as JSON
						if err := json.Unmarshal([]byte(args), &input); err != nil {
							input = map[string]interface{}{"raw": args}
						}
					} else if args, ok := contentMap["arguments"].(map[string]interface{}); ok {
						input = args
					}
				}
			}
			if input == nil {
				input = make(map[string]interface{})
			}
			blocks = append(blocks, types.ContentBlock{
				Type:  "tool_use",
				ID:    item.ID,
				Name:  name,
				Input: input,
			})
		}
	}

	return blocks
}

// extractContentFromPart extracts content from a content part object.
func extractContentFromPart(partMap map[string]interface{}) []types.ContentBlock {
	var blocks []types.ContentBlock

	partType, hasType := partMap["type"].(string)
	if !hasType {
		return blocks
	}

	switch partType {
	case "output_text", "text":
		// Text content
		if text, ok := partMap["text"].(string); ok && text != "" {
			blocks = append(blocks, types.ContentBlock{
				Type: "text",
				Text: text,
			})
		}
	case "refusal":
		// Refusal message
		if refusal, ok := partMap["refusal"].(string); ok && refusal != "" {
			blocks = append(blocks, types.ContentBlock{
				Type: "text",
				Text: "[Refusal] " + refusal,
			})
		}
	}

	return blocks
}

// translateResponsesStatus converts Responses API status to Anthropic stop_reason.
func translateResponsesStatus(status string) string {
	switch status {
	case "completed":
		return "end_turn"
	case "incomplete":
		return "max_tokens"
	case "cancelled":
		return "end_turn"
	case "failed":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// GenerateMessageID generates a unique message ID.
func GenerateMessageID() string {
	return "msg_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:24]
}

// MaxImageSize is the maximum allowed size for base64-decoded images (10MB).
const MaxImageSize = 10 * 1024 * 1024

// IsBase64Image checks if a string is a base64-encoded image data URL.
func IsBase64Image(s string) bool {
	return strings.HasPrefix(s, "data:image/")
}

// ParseBase64Image parses a base64 data URL into media type and data.
// Returns an error if the data is invalid or exceeds MaxImageSize.
func ParseBase64Image(dataURL string) (mediaType string, data string, err error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", "", fmt.Errorf("invalid data URL")
	}

	// Format: data:image/png;base64,<data>
	parts := strings.SplitN(dataURL[5:], ",", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid data URL format")
	}

	metaParts := strings.Split(parts[0], ";")
	if len(metaParts) < 2 || metaParts[1] != "base64" {
		return "", "", fmt.Errorf("not a base64 data URL")
	}

	mediaType = metaParts[0]
	data = parts[1]

	// Validate base64 and check size
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", "", fmt.Errorf("invalid base64 data: %w", err)
	}

	if len(decoded) > MaxImageSize {
		return "", "", fmt.Errorf("image too large: %d bytes (max %d)", len(decoded), MaxImageSize)
	}

	return mediaType, data, nil
}
