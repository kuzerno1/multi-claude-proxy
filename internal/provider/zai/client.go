// Package zai implements the Z.AI provider for Anthropic-compatible passthrough.
package zai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// Client handles HTTP communication with the Z.AI API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	modelsPath string
	quotaURL   string
}

// NewClient creates a new Z.AI API client.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: config.ZAITimeout,
		},
		baseURL:    config.ZAIBaseURL,
		modelsPath: config.ZAIModelsPath,
		quotaURL:   config.ZAIQuotaURL,
	}
}

// ModelsResponse represents the response from Z.AI's /v1/models endpoint.
type ModelsResponse struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

// ModelEntry represents a single model in the models response (Anthropic-compatible format).
type ModelEntry struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name"`
	CreatedAt   *string `json:"created_at"` // RFC 3339 datetime string, nil if not provided
	Type        string  `json:"type"`
	// Legacy fields for older API responses
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// QuotaResponse represents the response from Z.AI's quota endpoint.
type QuotaResponse struct {
	Code    int  `json:"code"`
	Success bool `json:"success"`
	Msg     string `json:"msg"`
	Data    struct {
		Limits []QuotaLimit `json:"limits"`
	} `json:"data"`
}

// QuotaLimit represents a single quota limit entry.
type QuotaLimit struct {
	Type          string `json:"type"`
	Unit          int    `json:"unit"`
	Number        int    `json:"number"`
	Usage         int64  `json:"usage"`         // Max available tokens
	CurrentValue  int64  `json:"currentValue"`  // Tokens used
	Remaining     int64  `json:"remaining"`     // Tokens remaining
	Percentage    int    `json:"percentage"`    // Percentage used (0-100)
	NextResetTime *int64 `json:"nextResetTime"` // Reset time in milliseconds (only present when tokens used)
}

// QuotaInfo contains parsed quota information.
type QuotaInfo struct {
	MaxTokens           int64
	UsedTokens          int64
	RemainingTokens     int64
	UsedPercentage      int
	RemainingPercentage int
	RemainingFraction   float64
	ResetTime           *time.Time // Parsed reset time (nil if no usage)
	ResetTimeMs         int64      // Raw reset time in milliseconds (0 if no usage)
}

// Response represents a response from the Z.AI API.
type Response struct {
	Data      *types.AnthropicResponse
	RawReader io.ReadCloser
	Body      []byte
}

// FetchModels fetches available models from the Z.AI API.
// Returns full model entries with display_name and created_at.
func (c *Client) FetchModels(ctx context.Context, apiKey string) ([]ModelEntry, error) {
	url := c.baseURL + c.modelsPath

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(config.ZAIAuthHeader, "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("authentication_error: invalid API key (status %d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	utils.Debug("[Z.AI] Fetched %d models", len(modelsResp.Data))
	return modelsResp.Data, nil
}

// FetchModelIDs fetches available model IDs from the Z.AI API.
// Returns only model IDs for backwards compatibility.
func (c *Client) FetchModelIDs(ctx context.Context, apiKey string) ([]string, error) {
	models, err := c.FetchModels(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids, nil
}

// FetchQuota fetches quota information from the Z.AI API.
func (c *Client) FetchQuota(ctx context.Context, apiKey string) (*QuotaInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.quotaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(config.ZAIAuthHeader, "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("authentication_error: invalid API key (status %d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var quotaResp QuotaResponse
	if err := json.NewDecoder(resp.Body).Decode(&quotaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !quotaResp.Success {
		return nil, fmt.Errorf("quota API returned error: %s", quotaResp.Msg)
	}

	// Find TOKENS_LIMIT
	for _, limit := range quotaResp.Data.Limits {
		if limit.Type == "TOKENS_LIMIT" {
			info := &QuotaInfo{
				MaxTokens:           limit.Usage,
				UsedTokens:          limit.CurrentValue,
				RemainingTokens:     limit.Remaining,
				UsedPercentage:      limit.Percentage,
				RemainingPercentage: 100 - limit.Percentage,
			}
			if limit.Usage > 0 {
				info.RemainingFraction = float64(limit.Remaining) / float64(limit.Usage)
			}
			// Parse nextResetTime if present (only when tokens have been used)
			if limit.NextResetTime != nil {
				info.ResetTimeMs = *limit.NextResetTime
				resetTime := time.UnixMilli(*limit.NextResetTime)
				info.ResetTime = &resetTime
			}
			return info, nil
		}
	}

	return nil, fmt.Errorf("TOKENS_LIMIT not found in quota response")
}

// SendMessage sends a non-streaming message request to the Z.AI API.
func (c *Client) SendMessage(ctx context.Context, apiKey string, anthropicReq *types.AnthropicRequest) (*types.AnthropicResponse, error) {
	// Ensure stream is false
	reqCopy := *anthropicReq
	reqCopy.Stream = false

	body, err := json.Marshal(reqCopy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	utils.Debug("[Z.AI] Sending non-streaming request to %s", url)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	return c.handleResponse(resp)
}

// SendMessageStream sends a streaming message request to the Z.AI API.
// Returns an io.ReadCloser for SSE parsing.
func (c *Client) SendMessageStream(ctx context.Context, apiKey string, anthropicReq *types.AnthropicRequest) (io.ReadCloser, error) {
	// Ensure stream is true
	reqCopy := *anthropicReq
	reqCopy.Stream = true

	body, err := json.Marshal(reqCopy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	utils.Debug("[Z.AI] Sending streaming request to %s", url)

	// Use a client without timeout for streaming
	streamClient := &http.Client{
		Timeout: 0, // No timeout for streaming
	}

	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Check for error responses
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, c.handleErrorResponse(resp)
	}

	return resp.Body, nil
}

// handleResponse processes a non-streaming response.
func (c *Client) handleResponse(resp *http.Response) (*types.AnthropicResponse, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, c.handleErrorResponse(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var anthropicResp types.AnthropicResponse
	if err := json.Unmarshal(body, &anthropicResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &anthropicResp, nil
}

// handleErrorResponse processes an error response from the API.
func (c *Client) handleErrorResponse(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("authentication_error: %s", string(body)),
		}
	case http.StatusTooManyRequests:
		// Try to parse rate limit info from response
		var errorResp struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		resetMs := int64(config.DefaultRateLimitResetMs) // Default 1 minute cooldown
		if json.Unmarshal(body, &errorResp) == nil {
			// Could parse retry-after header or response body for reset time
		}
		return &RateLimitError{
			ResetMs: resetMs,
			Message: fmt.Sprintf("rate_limit_error: %s", string(body)),
		}
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("server_error: %s", string(body)),
		}
	default:
		return &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("api_error: status %d, body: %s", resp.StatusCode, string(body)),
		}
	}
}

// HTTPStatusError represents an HTTP error with status code.
type HTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e *HTTPStatusError) Error() string {
	return e.Message
}

// RateLimitError represents a rate limit error.
type RateLimitError struct {
	ResetMs int64
	Message string
}

func (e *RateLimitError) Error() string {
	return e.Message
}

// VerifyAPIKey verifies that an API key is valid by calling the models endpoint.
func (c *Client) VerifyAPIKey(ctx context.Context, apiKey string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := c.FetchModels(ctx, apiKey)
	return err
}
