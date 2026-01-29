// Package copilot implements a provider for GitHub Copilot API.
package copilot

// ChatCompletionsPayload represents an OpenAI-compatible chat completions request.
type ChatCompletionsPayload struct {
	Model            string      `json:"model"`
	Messages         []Message   `json:"messages"`
	MaxTokens        int         `json:"max_tokens,omitempty"`
	Temperature      *float64    `json:"temperature,omitempty"`
	TopP             *float64    `json:"top_p,omitempty"`
	Stream           bool        `json:"stream,omitempty"`
	Stop             []string    `json:"stop,omitempty"`
	Tools            []Tool      `json:"tools,omitempty"`
	ToolChoice       interface{} `json:"tool_choice,omitempty"` // "auto", "none", "required", or ToolChoiceFunction
	FrequencyPenalty *float64    `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64    `json:"presence_penalty,omitempty"`
	User             string      `json:"user,omitempty"`
}

// ResponsesPayload represents an OpenAI Responses API request.
// This format is used for models that support the /responses endpoint.
type ResponsesPayload struct {
	Model           string          `json:"model"`
	Input           []ResponseInput `json:"input"`
	Instructions    string          `json:"instructions,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	Stop            []string        `json:"stop,omitempty"`
	Tools           []Tool          `json:"tools,omitempty"`
	ToolChoice      interface{}     `json:"tool_choice,omitempty"`
}

// ResponseInput represents a single input item in the Responses API.
type ResponseInput struct {
	Type       string      `json:"type"`               // Required: "message"
	Role       string      `json:"role"`               // "user", "assistant", "tool"
	Content    interface{} `json:"content,omitempty"`
	Name       string      `json:"name,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

// ResponsesAPIResponse represents a response from the /responses endpoint.
type ResponsesAPIResponse struct {
	ID           string                `json:"id"`
	Object       string                `json:"object"` // "response"
	CreatedAt    int64                 `json:"created_at"`
	Status       string                `json:"status"` // "completed", "in_progress", etc.
	Output       []ResponseOutputItem  `json:"output"`
	OutputText   string                `json:"output_text,omitempty"` // Convenience field for simple text
	Model        string                `json:"model"`
	Usage        *ResponsesUsage       `json:"usage,omitempty"`
	Error        *ResponsesError       `json:"error,omitempty"`
}

// ResponseOutputItem represents an output item from the Responses API.
type ResponseOutputItem struct {
	Type    string      `json:"type"` // "message", "function_call", etc.
	ID      string      `json:"id,omitempty"`
	Role    string      `json:"role,omitempty"`
	Content interface{} `json:"content,omitempty"`
	Status  string      `json:"status,omitempty"`
}

// ResponsesUsage contains token usage for Responses API.
type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ResponsesError represents an error in Responses API response.
type ResponsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResponsesStreamEvent represents a streaming event from the /responses endpoint.
type ResponsesStreamEvent struct {
	Type         string               `json:"type"` // "response.created", "response.output_item.added", "response.output_text.delta", etc.
	Response     *ResponsesAPIResponse `json:"response,omitempty"`
	OutputIndex  int                  `json:"output_index,omitempty"`
	ContentIndex int                  `json:"content_index,omitempty"`
	ItemID       string               `json:"item_id,omitempty"`
	Item         *ResponseOutputItem  `json:"item,omitempty"`
	Delta        string               `json:"delta,omitempty"`
}

// ToolChoiceFunction specifies a specific function to call.
type ToolChoiceFunction struct {
	Type     string               `json:"type"` // "function"
	Function ToolChoiceFunctionID `json:"function"`
}

// ToolChoiceFunctionID identifies a function by name.
type ToolChoiceFunctionID struct {
	Name string `json:"name"`
}

// Message represents a chat message in OpenAI format.
type Message struct {
	Role       string        `json:"role"` // "system", "user", "assistant", "tool"
	Content    interface{}   `json:"content,omitempty"`
	Name       string        `json:"name,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// ContentPart represents a part of multimodal content.
type ContentPart interface {
	isContentPart()
}

// TextPart represents text content.
type TextPart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

func (TextPart) isContentPart() {}

// ImagePart represents image content.
type ImagePart struct {
	Type     string   `json:"type"` // "image_url"
	ImageURL ImageURL `json:"image_url"`
}

func (ImagePart) isContentPart() {}

// ImageURL contains the URL or base64 data for an image.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "low", "high", "auto"
}

// Tool represents a function tool definition.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function FunctionDef  `json:"function"`
}

// FunctionDef defines a function's signature.
type FunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// ToolCall represents a tool invocation by the assistant.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ChatCompletionResponse represents a non-streaming response.
type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"` // "chat.completion"
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// Choice represents a completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop", "length", "tool_calls", "content_filter"
	Logprobs     interface{} `json:"logprobs"`
}

// Usage contains token usage information.
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails provides detailed prompt token breakdown.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// CompletionTokensDetails provides detailed completion token breakdown.
type CompletionTokensDetails struct {
	AcceptedPredictionTokens  int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens  int `json:"rejected_prediction_tokens,omitempty"`
}

// ChatCompletionChunk represents a streaming response chunk.
type ChatCompletionChunk struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"` // "chat.completion.chunk"
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Choices           []StreamChoice `json:"choices"`
	Usage             *Usage         `json:"usage,omitempty"`
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`
}

// StreamChoice represents a streaming choice.
type StreamChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
	Logprobs     interface{} `json:"logprobs"`
}

// Delta represents incremental content in streaming.
type Delta struct {
	Role      string           `json:"role,omitempty"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []ToolCallDelta  `json:"tool_calls,omitempty"`
}

// ToolCallDelta represents incremental tool call data.
type ToolCallDelta struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"` // "function"
	Function *FunctionCallDelta `json:"function,omitempty"`
}

// FunctionCallDelta represents incremental function call data.
type FunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ModelsResponse represents the response from the models endpoint.
type ModelsResponse struct {
	Data   []Model `json:"data"`
	Object string  `json:"object"`
}

// Model represents a model's metadata.
type Model struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Object             string            `json:"object"`
	Vendor             string            `json:"vendor"`
	Version            string            `json:"version"`
	Preview            bool              `json:"preview"`
	ModelPickerEnabled bool              `json:"model_picker_enabled"`
	Capabilities       ModelCapabilities `json:"capabilities"`
	Policy             *ModelPolicy      `json:"policy,omitempty"`
	SupportedEndpoints []string          `json:"supported_endpoints,omitempty"`
}

// DefaultEndpoint is the fallback endpoint when none is specified.
const DefaultEndpoint = "/chat/completions"

// PreferredEndpoint returns the preferred endpoint for this model.
// If SupportedEndpoints is empty, returns DefaultEndpoint.
// Otherwise returns the first supported endpoint.
func (m *Model) PreferredEndpoint() string {
	if len(m.SupportedEndpoints) == 0 {
		return DefaultEndpoint
	}
	return m.SupportedEndpoints[0]
}

// ModelCapabilities describes what a model can do.
type ModelCapabilities struct {
	Family    string        `json:"family"`
	Type      string        `json:"type"`
	Object    string        `json:"object"`
	Tokenizer string        `json:"tokenizer"`
	Limits    ModelLimits   `json:"limits"`
	Supports  ModelSupports `json:"supports"`
}

// ModelLimits defines token limits for a model.
type ModelLimits struct {
	MaxContextWindowTokens int `json:"max_context_window_tokens,omitempty"`
	MaxOutputTokens        int `json:"max_output_tokens,omitempty"`
	MaxPromptTokens        int `json:"max_prompt_tokens,omitempty"`
	MaxInputs              int `json:"max_inputs,omitempty"`
}

// ModelSupports describes supported features.
type ModelSupports struct {
	ToolCalls         bool `json:"tool_calls,omitempty"`
	ParallelToolCalls bool `json:"parallel_tool_calls,omitempty"`
	Dimensions        bool `json:"dimensions,omitempty"`
}

// ModelPolicy contains policy information.
type ModelPolicy struct {
	State string `json:"state"`
	Terms string `json:"terms"`
}

// DeviceCodeResponse represents the GitHub device code response.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// AccessTokenResponse represents the GitHub access token response.
type AccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

// CopilotTokenResponse represents the Copilot token exchange response.
type CopilotTokenResponse struct {
	Token        string `json:"token"`
	ExpiresAt    int64  `json:"expires_at"`
	RefreshIn    int    `json:"refresh_in"`
	ErrorDetails string `json:"error_details,omitempty"`
}

// GitHubUser represents a GitHub user profile.
type GitHubUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// AccountType represents the type of GitHub Copilot subscription.
type AccountType string

const (
	AccountTypeIndividual AccountType = "individual"
	AccountTypeBusiness   AccountType = "business"
	AccountTypeEnterprise AccountType = "enterprise"
)

// BaseURLForAccountType returns the Copilot API base URL for the given account type.
func BaseURLForAccountType(accountType AccountType) string {
	switch accountType {
	case AccountTypeBusiness:
		return "https://api.business.githubcopilot.com"
	case AccountTypeEnterprise:
		return "https://api.enterprise.githubcopilot.com"
	default:
		return "https://api.githubcopilot.com"
	}
}

// QuotaDetail represents quota information for a specific Copilot feature.
type QuotaDetail struct {
	Entitlement      float64 `json:"entitlement"`
	OverageCount     float64 `json:"overage_count"`
	OveragePermitted bool    `json:"overage_permitted"`
	PercentRemaining float64 `json:"percent_remaining"`
	QuotaID          string  `json:"quota_id"`
	QuotaRemaining   float64 `json:"quota_remaining"`
	Remaining        float64 `json:"remaining"`
	Unlimited        bool    `json:"unlimited"`
}

// QuotaSnapshots contains quota information for all Copilot features.
type QuotaSnapshots struct {
	Chat                *QuotaDetail `json:"chat,omitempty"`
	Completions         *QuotaDetail `json:"completions,omitempty"`
	PremiumInteractions *QuotaDetail `json:"premium_interactions,omitempty"`
}

// CopilotUsageResponse represents the response from the Copilot usage API.
type CopilotUsageResponse struct {
	AccessTypeSku        string         `json:"access_type_sku"`
	AnalyticsTrackingID  string         `json:"analytics_tracking_id"`
	AssignedDate         string         `json:"assigned_date"`
	CanSignupForLimited  bool           `json:"can_signup_for_limited"`
	ChatEnabled          bool           `json:"chat_enabled"`
	CopilotPlan          string         `json:"copilot_plan"`
	QuotaResetDate       string         `json:"quota_reset_date"`
	QuotaSnapshots       QuotaSnapshots `json:"quota_snapshots"`
}
