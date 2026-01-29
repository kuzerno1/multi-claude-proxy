package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	// Copilot API constants
	CopilotVersion       = "0.26.7"
	EditorPluginVersion  = "copilot-chat/" + CopilotVersion
	UserAgent            = "GitHubCopilotChat/" + CopilotVersion
	VSCodeVersion        = "1.96.0"

	// Default timeout for API requests
	DefaultTimeout = 10 * time.Minute
)

// Client handles HTTP communication with the GitHub Copilot API.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient creates a new Copilot API client.
func NewClient(accountType AccountType) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		baseURL: BaseURLForAccountType(accountType),
	}
}

// NewClientWithBaseURL creates a client with a custom base URL (for testing).
func NewClientWithBaseURL(baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		baseURL: baseURL,
	}
}

// copilotHeaders returns the required headers for Copilot API requests.
func (c *Client) copilotHeaders(copilotToken string, vision bool) map[string]string {
	headers := map[string]string{
		"Authorization":                       fmt.Sprintf("Bearer %s", copilotToken),
		"Content-Type":                        "application/json",
		"Accept":                              "application/json",
		"Copilot-Integration-Id":              "vscode-chat",
		"Editor-Version":                      fmt.Sprintf("vscode/%s", VSCodeVersion),
		"Editor-Plugin-Version":               EditorPluginVersion,
		"User-Agent":                          UserAgent,
		"Openai-Intent":                       "conversation-panel",
		"X-Request-Id":                        uuid.New().String(),
		"X-Vscode-User-Agent-Library-Version": "electron-fetch",
	}

	if vision {
		headers["Copilot-Vision-Request"] = "true"
	}

	return headers
}

// SendMessage sends a non-streaming chat completion request.
// The endpoint parameter specifies which API endpoint to use (e.g., "/chat/completions", "/responses").
// payload can be *ChatCompletionsPayload or *ResponsesPayload.
// Returns *ChatCompletionResponse for /chat/completions or *ResponsesAPIResponse for /responses.
func (c *Client) SendMessage(ctx context.Context, copilotToken string, payload interface{}, endpoint string) (interface{}, error) {
	// Set streaming flag based on payload type
	switch p := payload.(type) {
	case *ChatCompletionsPayload:
		p.Stream = false
	case *ResponsesPayload:
		p.Stream = false
	default:
		return nil, fmt.Errorf("unsupported payload type: %T", payload)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Check for vision content
	vision := hasVisionContent(payload)
	for k, v := range c.copilotHeaders(copilotToken, vision) {
		req.Header.Set(k, v)
	}

	// Set X-Initiator header based on message roles
	req.Header.Set("X-Initiator", getInitiator(payload))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleErrorResponse(resp); err != nil {
		return nil, err
	}

	// Decode based on endpoint type
	if endpoint == "/responses" {
		// Read the raw body for debugging
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read responses API response body: %w", err)
		}

		// Debug log the raw response
		fmt.Printf("[Copilot DEBUG] Raw /responses body (first 500 chars): %.500s\n", string(bodyBytes))

		var result ResponsesAPIResponse
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to decode responses API response: %w", err)
		}

		// Debug log the decoded response
		fmt.Printf("[Copilot DEBUG] Decoded response - ID: %.50s..., OutputText: %.100s, Output items: %d\n",
			result.ID, result.OutputText, len(result.Output))
		if len(result.Output) > 0 {
			for i, item := range result.Output {
				fmt.Printf("[Copilot DEBUG]   Output[%d] - Type: %s, Role: %s, Content: %T\n",
					i, item.Type, item.Role, item.Content)
			}
		}

		return &result, nil
	}

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// SendMessageStream sends a streaming chat completion request.
// The endpoint parameter specifies which API endpoint to use (e.g., "/chat/completions", "/responses").
// payload can be *ChatCompletionsPayload or *ResponsesPayload.
// Returns an io.ReadCloser for reading SSE events.
func (c *Client) SendMessageStream(ctx context.Context, copilotToken string, payload interface{}, endpoint string) (io.ReadCloser, error) {
	// Set streaming flag based on payload type
	switch p := payload.(type) {
	case *ChatCompletionsPayload:
		p.Stream = true
	case *ResponsesPayload:
		p.Stream = true
	default:
		return nil, fmt.Errorf("unsupported payload type: %T", payload)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Check for vision content
	vision := hasVisionContent(payload)
	for k, v := range c.copilotHeaders(copilotToken, vision) {
		req.Header.Set(k, v)
	}

	// For streaming, accept SSE
	req.Header.Set("Accept", "text/event-stream")

	// Set X-Initiator header based on message roles
	req.Header.Set("X-Initiator", getInitiator(payload))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if err := c.handleErrorResponse(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}

	return resp.Body, nil
}

// GetModels fetches the list of available models.
func (c *Client) GetModels(ctx context.Context, copilotToken string) (*ModelsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for k, v := range c.copilotHeaders(copilotToken, false) {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleErrorResponse(resp); err != nil {
		return nil, err
	}

	var result ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// handleErrorResponse checks for error responses and returns appropriate errors.
func (c *Client) handleErrorResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Try to extract error message from JSON response
	var errorDetail string
	var jsonErr struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &jsonErr); err == nil {
		if jsonErr.Message != "" {
			errorDetail = jsonErr.Message
		} else if jsonErr.Error != "" {
			errorDetail = jsonErr.Error
		}
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		msg := "unauthorized: token may be expired or invalid"
		if errorDetail != "" {
			msg = fmt.Sprintf("unauthorized: %s", errorDetail)
		}
		return &AuthError{Message: msg, StatusCode: resp.StatusCode}
	case http.StatusForbidden:
		msg := "forbidden: access denied"
		if errorDetail != "" {
			msg = fmt.Sprintf("forbidden: %s", errorDetail)
		}
		return &AuthError{Message: msg, StatusCode: resp.StatusCode}
	case http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		msg := "rate limit exceeded"
		if errorDetail != "" {
			msg = fmt.Sprintf("rate limit exceeded: %s", errorDetail)
		}
		return &RateLimitError{
			Message:    msg,
			RetryAfter: retryAfter,
			StatusCode: resp.StatusCode,
		}
	default:
		msg := fmt.Sprintf("request failed with status %d", resp.StatusCode)
		if errorDetail != "" {
			msg = fmt.Sprintf("request failed with status %d: %s", resp.StatusCode, errorDetail)
		} else if len(bodyStr) > 0 && len(bodyStr) < 500 {
			msg = fmt.Sprintf("request failed with status %d: %s", resp.StatusCode, bodyStr)
		}
		return &HTTPError{
			Message:    msg,
			StatusCode: resp.StatusCode,
		}
	}
}

// parseRetryAfter parses the Retry-After header value.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 60 * time.Second // Default to 60 seconds
	}

	// Try parsing as seconds
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try parsing as HTTP date
	if t, err := http.ParseTime(value); err == nil {
		return time.Until(t)
	}

	return 60 * time.Second
}

// hasVisionContent checks if the payload contains any image content.
// Works with both ChatCompletionsPayload and ResponsesPayload.
func hasVisionContent(payload interface{}) bool {
	switch p := payload.(type) {
	case *ChatCompletionsPayload:
		return hasVisionContentInMessages(p.Messages)
	case *ResponsesPayload:
		return hasVisionContentInInputs(p.Input)
	default:
		return false
	}
}

// hasVisionContentInMessages checks Chat Completions messages for images.
func hasVisionContentInMessages(messages []Message) bool {
	for _, msg := range messages {
		if parts, ok := msg.Content.([]interface{}); ok {
			for _, part := range parts {
				if m, ok := part.(map[string]interface{}); ok {
					if m["type"] == "image_url" {
						return true
					}
				}
			}
		}
	}
	return false
}

// hasVisionContentInInputs checks Responses API inputs for images.
func hasVisionContentInInputs(inputs []ResponseInput) bool {
	for _, input := range inputs {
		if parts, ok := input.Content.([]interface{}); ok {
			for _, part := range parts {
				if m, ok := part.(map[string]interface{}); ok {
					if m["type"] == "image_url" {
						return true
					}
				}
			}
		}
	}
	return false
}

// getInitiator determines if this is an agent call based on message roles.
// Works with both ChatCompletionsPayload and ResponsesPayload.
func getInitiator(payload interface{}) string {
	switch p := payload.(type) {
	case *ChatCompletionsPayload:
		for _, msg := range p.Messages {
			if msg.Role == "assistant" || msg.Role == "tool" {
				return "agent"
			}
		}
	case *ResponsesPayload:
		for _, input := range p.Input {
			if input.Role == "assistant" || input.Role == "tool" {
				return "agent"
			}
		}
	}
	return "user"
}

// Error types

// HTTPError represents a general HTTP error.
type HTTPError struct {
	Message    string
	StatusCode int
}

func (e *HTTPError) Error() string {
	return e.Message
}

// AuthError represents an authentication error.
type AuthError struct {
	Message    string
	StatusCode int
}

func (e *AuthError) Error() string {
	return e.Message
}

// RateLimitError represents a rate limit error.
type RateLimitError struct {
	Message    string
	RetryAfter time.Duration
	StatusCode int
}

func (e *RateLimitError) Error() string {
	return e.Message
}

// RetryAfterMs returns the retry-after duration in milliseconds.
func (e *RateLimitError) RetryAfterMs() int64 {
	return e.RetryAfter.Milliseconds()
}
