// Package types defines the Anthropic API structures used across all providers.
// These types are the canonical format that the proxy uses internally.
// Each provider is responsible for converting to/from these types.
package types

import (
	"encoding/json"
	"time"
)

// AnthropicRequest represents an Anthropic Messages API request.
type AnthropicRequest struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	System        json.RawMessage `json:"system,omitempty"` // Can be string or []SystemBlock
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    *ToolChoice     `json:"tool_choice,omitempty"`
	Thinking      *ThinkingConfig `json:"thinking,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
}

// Message represents a conversation message.
type Message struct {
	Role    string          `json:"role"`    // "user" or "assistant"
	Content json.RawMessage `json:"content"` // Can be string or []ContentBlock
}

// ContentBlock represents a block of content in a message.
// This is a union type - only one of the fields will be populated based on Type.
type ContentBlock struct {
	Type string `json:"type"`

	// Text block fields
	Text string `json:"text,omitempty"`

	// Thinking block fields
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// Redacted thinking block fields (Node parity)
	Data string `json:"data,omitempty"`

	// Tool use block fields
	ID               string                 `json:"id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Input            map[string]interface{} `json:"input,omitempty"`
	ThoughtSignature string                 `json:"thoughtSignature,omitempty"` // For Gemini 3+ thinking models

	// Tool result block fields
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // Can be string or []ContentBlock
	IsError   bool            `json:"is_error,omitempty"`

	// Image block fields
	Source *ImageSource `json:"source,omitempty"`
}

// ImageSource represents the source of an image in a content block.
type ImageSource struct {
	Type      string `json:"type"` // "base64" or "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// SystemBlock represents a block in the system prompt.
type SystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl specifies caching behavior for content.
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// Tool represents a tool definition.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema,omitempty"`
	Function    *FunctionDefinition    `json:"function,omitempty"` // OpenAI-style function
}

// FunctionDefinition represents an OpenAI-style function definition.
type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// ToolChoice specifies how the model should use tools.
type ToolChoice struct {
	Type string `json:"type"`           // "auto", "any", "tool", "none"
	Name string `json:"name,omitempty"` // Required when type is "tool"
}

// ThinkingConfig configures thinking/reasoning for supported models.
type ThinkingConfig struct {
	Type         string `json:"type,omitempty"` // "enabled" or "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// AnthropicResponse represents an Anthropic Messages API response.
type AnthropicResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"` // "message"
	Role         string         `json:"role"` // "assistant"
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason,omitempty"` // "end_turn", "max_tokens", "tool_use"
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        Usage          `json:"usage"`
}

// Usage contains token usage information.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// AnthropicError represents an error response from the API.
type AnthropicError struct {
	Type  string      `json:"type"` // "error"
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error details.
type ErrorDetail struct {
	Type    string `json:"type"` // "invalid_request_error", "authentication_error", etc.
	Message string `json:"message"`
}

// StreamEvent represents an SSE event in the Anthropic streaming format.
type StreamEvent struct {
	Type string `json:"type"`

	// Raw allows providers to bypass struct-based marshaling and emit
	// Node-parity JSON shapes (including explicit nulls/zero values).
	// It is not serialized directly.
	Raw any `json:"-"`

	// message_start
	Message *AnthropicResponse `json:"message,omitempty"`

	// content_block_start
	Index        int           `json:"index,omitempty"`
	ContentBlock *ContentBlock `json:"content_block,omitempty"`

	// content_block_delta
	Delta *Delta `json:"delta,omitempty"`

	// message_delta
	Usage *Usage `json:"usage,omitempty"`

	// error
	Error *ErrorDetail `json:"error,omitempty"`
}

// Delta represents incremental content in a streaming response.
type Delta struct {
	Type         string `json:"type,omitempty"` // "text_delta", "thinking_delta", "input_json_delta", "signature_delta"
	Text         string `json:"text,omitempty"`
	Thinking     string `json:"thinking,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
	Signature    string `json:"signature,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}

// ModelsResponse represents the Anthropic-compatible response from the models endpoint.
type ModelsResponse struct {
	Data    []Model `json:"data"`
	FirstID string  `json:"first_id"`
	HasMore bool    `json:"has_more"`
	LastID  string  `json:"last_id"`
}

// Model represents a model in the Anthropic-compatible models list.
type Model struct {
	ID          string  `json:"id"`
	CreatedAt   *string `json:"created_at"` // RFC 3339 datetime string, null if unknown
	DisplayName string  `json:"display_name"`
	Type        string  `json:"type"` // Always "model"
}

// ModelInfo represents detailed model information for the models endpoint.
type ModelInfo struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name,omitempty"`
	Type            string `json:"type"`
	CreatedAt       string `json:"created_at,omitempty"`
	ContextSize     int    `json:"context_size,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

// ProviderStatus represents health and quota information for a provider.
type ProviderStatus struct {
	Name      string          `json:"name"`
	Status    string          `json:"status"` // "ok", "rate-limited", "error"
	Accounts  []AccountStatus `json:"accounts,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// AccountStatus represents the status of an individual account.
type AccountStatus struct {
	Email    string                `json:"email"`
	Status   string                `json:"status"` // "ok", "rate-limited", "invalid", "error"
	Error    string                `json:"error,omitempty"`
	LastUsed *time.Time            `json:"last_used,omitempty"`
	Limits   map[string]ModelQuota `json:"limits"`
}

// ModelQuota represents quota information for a specific model.
type ModelQuota struct {
	RemainingFraction   float64    `json:"remaining_fraction"`   // 0.0 to 1.0
	RemainingPercentage int        `json:"remaining_percentage"` // 0 to 100
	ResetTime           *time.Time `json:"reset_time,omitempty"`
}

// ParseMessageContent parses the Content field of a Message.
// Returns a slice of ContentBlock, handling both string and array formats.
func ParseMessageContent(content json.RawMessage) ([]ContentBlock, error) {
	if len(content) == 0 {
		return nil, nil
	}

	// Try to parse as string first
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		return []ContentBlock{{Type: "text", Text: str}}, nil
	}

	// Parse as array of content blocks
	var blocks []ContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// ParseSystemPrompt parses the System field of an AnthropicRequest.
// Returns a slice of SystemBlock, handling both string and array formats.
func ParseSystemPrompt(system json.RawMessage) ([]SystemBlock, error) {
	if len(system) == 0 {
		return nil, nil
	}

	// Try to parse as string first
	var str string
	if err := json.Unmarshal(system, &str); err == nil {
		return []SystemBlock{{Type: "text", Text: str}}, nil
	}

	// Parse as array of system blocks
	var blocks []SystemBlock
	if err := json.Unmarshal(system, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}
