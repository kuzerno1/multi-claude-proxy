package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
)

// Client handles HTTP requests to the Cloud Code API.
type Client struct {
	httpClient *http.Client
	endpoints  []string
}

// NewClient creates a new Cloud Code API client.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
		endpoints: config.AntigravityEndpointFallbacks,
	}
}

// RequestOptions contains options for an API request.
type RequestOptions struct {
	Token     string
	ProjectID string
	Model     string
	Payload   map[string]interface{}
	Stream    bool
}

// Response wraps the HTTP response with parsed data.
type Response struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	Data       map[string]interface{}
	RawReader  io.ReadCloser // For streaming responses
}

// HTTPStatusError represents a non-rate-limit HTTP error from the Cloud Code API.
// This lets callers make decisions based on the status code (Node parity).
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// DoRequest sends a request to the Cloud Code API with endpoint fallback.
func (c *Client) DoRequest(ctx context.Context, opts RequestOptions) (*Response, error) {
	var lastErr error
	var lastRateLimitErr *RateLimitError

	for _, endpoint := range c.endpoints {
		resp, err := c.doSingleRequest(ctx, endpoint, opts)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Node parity: on 429/resource exhausted, try the next endpoint first (DAILY -> PROD),
		// keeping the minimum reset time across all 429s.
		if rl, ok := err.(*RateLimitError); ok {
			if lastRateLimitErr == nil || (rl.ResetMs > 0 && (lastRateLimitErr.ResetMs == 0 || rl.ResetMs < lastRateLimitErr.ResetMs)) {
				lastRateLimitErr = rl
			}
			utils.Debug("[CloudCode] Rate limited at %s, trying next endpoint...", endpoint)
			continue
		}

		// Node parity: try the next endpoint on non-429 HTTP errors (including 4xx),
		// waiting briefly only for 5xx errors.
		if se, ok := err.(*HTTPStatusError); ok {
			if se.StatusCode >= 500 {
				utils.Warn("[CloudCode] %d error at %s, trying next endpoint...", se.StatusCode, endpoint)
				select {
				case <-time.After(1 * time.Second):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			} else {
				utils.Warn("[CloudCode] %d error at %s, trying next endpoint...", se.StatusCode, endpoint)
			}
			continue
		}

		// Try next endpoint for transient network errors.
		if isRetryableError(err) {
			utils.Warn("[CloudCode] Request failed at %s, trying next endpoint: %v", endpoint, err)
			continue
		}

		// Non-retryable error, return immediately.
		return nil, err
	}

	if lastRateLimitErr != nil {
		return nil, lastRateLimitErr
	}

	// Node parity: upstream HTTP errors are surfaced directly as "API error <status>: ...".
	// "All endpoints failed" is reserved for connectivity / non-HTTP failures.
	if lastErr != nil {
		if _, ok := lastErr.(*HTTPStatusError); ok {
			return nil, lastErr
		}
	}
	return nil, fmt.Errorf("All endpoints failed: %w", lastErr)
}

func (c *Client) doSingleRequest(ctx context.Context, endpoint string, opts RequestOptions) (*Response, error) {
	// Node parity:
	// - Streaming always uses the SSE endpoint.
	// - Non-streaming thinking models also use the SSE endpoint to preserve thinking blocks.
	// - Non-thinking non-streaming uses generateContent.
	useSSE := opts.Stream || config.IsThinkingModel(opts.Model)

	url := ""
	if useSSE {
		url = fmt.Sprintf("%s/v1internal:streamGenerateContent?alt=sse", endpoint)
	} else {
		url = fmt.Sprintf("%s/v1internal:generateContent", endpoint)
	}

	// Build request body
	body, err := json.Marshal(opts.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	headers := buildHeaders(opts.Token, opts.Model, useSSE)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Check for error status
	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		errResp := &Response{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header,
			Body:       bodyBytes,
		}

		// Parse rate limit info
		if resp.StatusCode == 429 || isResourceExhausted(bodyBytes) {
			resetMs := ParseResetTime(resp, string(bodyBytes))
			return errResp, &RateLimitError{
				Message: string(bodyBytes),
				ResetMs: resetMs,
			}
		}

		return errResp, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
		}
	}

	if useSSE {
		return &Response{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header,
			RawReader:  resp.Body,
		}, nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		// Try parsing as SSE if JSON fails
		data = nil
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       bodyBytes,
		Data:       data,
	}, nil
}

func buildHeaders(token, model string, stream bool) map[string]string {
	headers := make(map[string]string)
	headers["Authorization"] = "Bearer " + token
	headers["Content-Type"] = "application/json"

	// Add Antigravity headers
	for k, v := range config.GetAntigravityHeaders() {
		headers[k] = v
	}

	modelFamily := config.GetModelFamily(model)

	// Add interleaved thinking header for Claude thinking models
	if modelFamily == "claude" && config.IsThinkingModel(model) {
		headers["anthropic-beta"] = "interleaved-thinking-2025-05-14"
	}

	if stream {
		headers["Accept"] = "text/event-stream"
	}

	return headers
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "temporary failure")
}

func isResourceExhausted(body []byte) bool {
	return bytes.Contains(body, []byte("RESOURCE_EXHAUSTED")) ||
		bytes.Contains(body, []byte("quota")) ||
		bytes.Contains(body, []byte("rate limit"))
}

// RateLimitError represents a rate limit error with reset time.
type RateLimitError struct {
	Message string
	ResetMs int64
}

func (e *RateLimitError) Error() string {
	return e.Message
}

// ParseResetTime extracts reset time from response headers or error body.
func ParseResetTime(resp *http.Response, errorText string) int64 {
	var resetMs int64

	if resp != nil {
		// Check Retry-After header
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				resetMs = int64(seconds * 1000)
				utils.Debug("[CloudCode] Retry-After header: %ds", seconds)
			}
		}

		// Check x-ratelimit-reset header
		if resetMs == 0 {
			if ratelimitReset := resp.Header.Get("x-ratelimit-reset"); ratelimitReset != "" {
				if resetTime, err := strconv.ParseInt(ratelimitReset, 10, 64); err == nil {
					resetMs = (resetTime * 1000) - time.Now().UnixMilli()
					if resetMs > 0 {
						utils.Debug("[CloudCode] x-ratelimit-reset: %s", time.UnixMilli(resetTime*1000).Format(time.RFC3339))
					} else {
						resetMs = 0
					}
				}
			}
		}

		// Check x-ratelimit-reset-after header
		if resetMs == 0 {
			if resetAfter := resp.Header.Get("x-ratelimit-reset-after"); resetAfter != "" {
				if seconds, err := strconv.Atoi(resetAfter); err == nil && seconds > 0 {
					resetMs = int64(seconds * 1000)
					utils.Debug("[CloudCode] x-ratelimit-reset-after: %ds", seconds)
				}
			}
		}
	}

	// Parse from error body if not found in headers
	if resetMs == 0 && errorText != "" {
		resetMs = parseResetFromBody(errorText)
	}

	// Enforce minimum
	if resetMs > 0 && resetMs < 1000 {
		utils.Debug("[CloudCode] Reset time too small (%dms), enforcing 2s buffer", resetMs)
		resetMs = 2000
	}

	return resetMs
}

func parseResetFromBody(msg string) int64 {
	// Try quotaResetDelay (e.g. "754.431528ms" or "1.5s")
	quotaDelayRe := regexp.MustCompile(`quotaResetDelay[:\s"]+(\d+(?:\.\d+)?)(ms|s)`)
	if matches := quotaDelayRe.FindStringSubmatch(msg); len(matches) == 3 {
		value, _ := strconv.ParseFloat(matches[1], 64)
		unit := strings.ToLower(matches[2])
		if unit == "s" {
			return int64(value * 1000)
		}
		return int64(value)
	}

	// Try quotaResetTimeStamp (ISO format)
	quotaTimestampRe := regexp.MustCompile(`quotaResetTimeStamp[:\s"]+(\d{4}-\d{2}-\d{2}T[\d:.]+Z?)`)
	if matches := quotaTimestampRe.FindStringSubmatch(msg); len(matches) == 2 {
		if t, err := time.Parse(time.RFC3339, matches[1]); err == nil {
			return t.UnixMilli() - time.Now().UnixMilli()
		}
	}

	// Try duration format (1h23m45s, 23m45s, 45s)
	durationRe := regexp.MustCompile(`(\d+)h(\d+)m(\d+)s|(\d+)m(\d+)s|(\d+)s`)
	if matches := durationRe.FindStringSubmatch(msg); len(matches) > 0 {
		if matches[1] != "" {
			hours, _ := strconv.Atoi(matches[1])
			minutes, _ := strconv.Atoi(matches[2])
			seconds, _ := strconv.Atoi(matches[3])
			return int64((hours*3600 + minutes*60 + seconds) * 1000)
		} else if matches[4] != "" {
			minutes, _ := strconv.Atoi(matches[4])
			seconds, _ := strconv.Atoi(matches[5])
			return int64((minutes*60 + seconds) * 1000)
		} else if matches[6] != "" {
			seconds, _ := strconv.Atoi(matches[6])
			return int64(seconds * 1000)
		}
	}

	// Try "retry after X seconds"
	retrySecondsRe := regexp.MustCompile(`retry\s+(?:after\s+)?(\d+)\s*(?:sec|s\b)`)
	if matches := retrySecondsRe.FindStringSubmatch(msg); len(matches) == 2 {
		seconds, _ := strconv.Atoi(matches[1])
		return int64(seconds * 1000)
	}

	return 0
}

// ModelQuotaInfo represents quota information for a model.
type ModelQuotaInfo struct {
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
}

// AvailableModelsResponse is the response from fetchAvailableModels API.
type AvailableModelsResponse struct {
	Models map[string]struct {
		DisplayName string `json:"displayName"`
		QuotaInfo   *struct {
			RemainingFraction *float64 `json:"remainingFraction"`
			ResetTime         *string  `json:"resetTime"`
		} `json:"quotaInfo"`
	} `json:"models"`
	ModelOrder []string `json:"-"`
}

// FetchAvailableModels fetches model information including quota from the Cloud Code API.
func (c *Client) FetchAvailableModels(ctx context.Context, token string) (*AvailableModelsResponse, error) {
	headers := config.GetAntigravityHeaders()
	headers["Authorization"] = "Bearer " + token
	headers["Content-Type"] = "application/json"

	for _, endpoint := range c.endpoints {
		url := endpoint + "/v1internal:fetchAvailableModels"

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader([]byte("{}")))
		if err != nil {
			continue
		}

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Don't warn on context cancellation/deadline - expected on client disconnect, timeout, or shutdown
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			utils.Warn("[CloudCode] fetchAvailableModels failed at %s: %v", endpoint, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			utils.Warn("[CloudCode] fetchAvailableModels error at %s: %d - %s", endpoint, resp.StatusCode, string(body))
			continue
		}

		var result AvailableModelsResponse
		if err := json.Unmarshal(body, &result); err != nil {
			utils.Warn("[CloudCode] fetchAvailableModels decode error: %v", err)
			continue
		}

		// Node parity: preserve model key order from the JSON response.
		if order, err := parseModelsOrder(body); err == nil && len(order) > 0 {
			result.ModelOrder = order
		}

		return &result, nil
	}

	return nil, fmt.Errorf("failed to fetch available models from all endpoints")
}

func parseModelsOrder(body []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))

	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key")
		}

		if key != "models" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
			continue
		}

		// models: { "<modelId>": {...}, ... }
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if d, ok := tok.(json.Delim); !ok || d != '{' {
			return nil, fmt.Errorf("expected models object")
		}

		order := make([]string, 0)
		for dec.More() {
			modelKeyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			modelID, ok := modelKeyTok.(string)
			if !ok {
				return nil, fmt.Errorf("expected model key string")
			}
			order = append(order, modelID)

			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
		}

		// Consume closing "}" for models.
		if _, err := dec.Token(); err != nil {
			return nil, err
		}

		return order, nil
	}

	return nil, fmt.Errorf("models not found")
}
