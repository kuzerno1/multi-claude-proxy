package copilot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	client := NewClient(AccountTypeIndividual)

	expectedURL := "https://api.githubcopilot.com"
	if client.baseURL != expectedURL {
		t.Errorf("expected base URL %s for individual, got %s", expectedURL, client.baseURL)
	}

	if client.httpClient == nil {
		t.Error("expected HTTP client to be initialized")
	}

	if client.httpClient.Timeout != DefaultTimeout {
		t.Errorf("expected timeout %v, got %v", DefaultTimeout, client.httpClient.Timeout)
	}
}

func TestNewClient_AccountTypes(t *testing.T) {
	tests := []struct {
		accountType AccountType
		expectedURL string
	}{
		{AccountTypeIndividual, "https://api.githubcopilot.com"},
		{AccountTypeBusiness, "https://api.business.githubcopilot.com"},
		{AccountTypeEnterprise, "https://api.enterprise.githubcopilot.com"},
	}

	for _, test := range tests {
		t.Run(string(test.accountType), func(t *testing.T) {
			client := NewClient(test.accountType)
			if client.baseURL != test.expectedURL {
				t.Errorf("expected base URL %s, got %s", test.expectedURL, client.baseURL)
			}
		})
	}
}

func TestNewClientWithBaseURL(t *testing.T) {
	customURL := "https://custom.api.example.com"
	client := NewClientWithBaseURL(customURL)

	if client.baseURL != customURL {
		t.Errorf("expected base URL %s, got %s", customURL, client.baseURL)
	}
}

func TestClient_HandleErrorResponse_Success(t *testing.T) {
	client := NewClient(AccountTypeIndividual)

	tests := []int{200, 201, 204, 299}
	for _, statusCode := range tests {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			resp := &http.Response{
				StatusCode: statusCode,
			}
			err := client.handleErrorResponse(resp)
			if err != nil {
				t.Errorf("expected no error for status %d, got %v", statusCode, err)
			}
		})
	}
}

func TestClient_HandleErrorResponse_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"message": "Token expired"})
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL)
	resp, _ := http.Get(server.URL)
	defer resp.Body.Close()

	err := client.handleErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}

	authErr, ok := err.(*AuthError)
	if !ok {
		t.Fatalf("expected AuthError, got %T", err)
	}

	if authErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status code 401, got %d", authErr.StatusCode)
	}

	if authErr.Message != "unauthorized: Token expired" {
		t.Errorf("expected message to contain 'Token expired', got %s", authErr.Message)
	}
}

func TestClient_HandleErrorResponse_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"message": "No subscription"})
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL)
	resp, _ := http.Get(server.URL)
	defer resp.Body.Close()

	err := client.handleErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}

	authErr, ok := err.(*AuthError)
	if !ok {
		t.Fatalf("expected AuthError, got %T", err)
	}

	if authErr.StatusCode != http.StatusForbidden {
		t.Errorf("expected status code 403, got %d", authErr.StatusCode)
	}
}

func TestClient_HandleErrorResponse_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL)
	resp, _ := http.Get(server.URL)
	defer resp.Body.Close()

	err := client.handleErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}

	rateErr, ok := err.(*RateLimitError)
	if !ok {
		t.Fatalf("expected RateLimitError, got %T", err)
	}

	if rateErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status code 429, got %d", rateErr.StatusCode)
	}

	if rateErr.RetryAfter != 30*time.Second {
		t.Errorf("expected retry after 30s, got %v", rateErr.RetryAfter)
	}
}

func TestClient_HandleErrorResponse_GenericError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Bad request body"))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL)
	resp, _ := http.Get(server.URL)
	defer resp.Body.Close()

	err := client.handleErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}

	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}

	if httpErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status code 400, got %d", httpErr.StatusCode)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected time.Duration
	}{
		{"empty", "", 60 * time.Second},
		{"seconds", "30", 30 * time.Second},
		{"zero", "0", 0},
		{"invalid", "not-a-number", 60 * time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := parseRetryAfter(test.value)
			if result != test.expected {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
		})
	}
}

func TestHasVisionContent(t *testing.T) {
	tests := []struct {
		name     string
		payload  *ChatCompletionsPayload
		expected bool
	}{
		{
			name: "text only",
			payload: &ChatCompletionsPayload{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			expected: false,
		},
		{
			name: "with image",
			payload: &ChatCompletionsPayload{
				Messages: []Message{
					{
						Role: "user",
						Content: []interface{}{
							map[string]interface{}{"type": "text", "text": "What's in this image?"},
							map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/img.png"}},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := hasVisionContent(test.payload)
			if result != test.expected {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
		})
	}
}

func TestGetInitiator(t *testing.T) {
	tests := []struct {
		name     string
		payload  *ChatCompletionsPayload
		expected string
	}{
		{
			name: "user only",
			payload: &ChatCompletionsPayload{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			expected: "user",
		},
		{
			name: "with assistant",
			payload: &ChatCompletionsPayload{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
					{Role: "assistant", Content: "Hi there"},
				},
			},
			expected: "agent",
		},
		{
			name: "with tool",
			payload: &ChatCompletionsPayload{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
					{Role: "tool", Content: "result"},
				},
			},
			expected: "agent",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := getInitiator(test.payload)
			if result != test.expected {
				t.Errorf("expected %s, got %s", test.expected, result)
			}
		})
	}
}

func TestErrorTypes(t *testing.T) {
	t.Run("HTTPError", func(t *testing.T) {
		err := &HTTPError{Message: "test error", StatusCode: 500}
		if err.Error() != "test error" {
			t.Errorf("expected 'test error', got %s", err.Error())
		}
	})

	t.Run("AuthError", func(t *testing.T) {
		err := &AuthError{Message: "auth failed", StatusCode: 401}
		if err.Error() != "auth failed" {
			t.Errorf("expected 'auth failed', got %s", err.Error())
		}
	})

	t.Run("RateLimitError", func(t *testing.T) {
		err := &RateLimitError{Message: "rate limited", RetryAfter: 30 * time.Second, StatusCode: 429}
		if err.Error() != "rate limited" {
			t.Errorf("expected 'rate limited', got %s", err.Error())
		}
		if err.RetryAfterMs() != 30000 {
			t.Errorf("expected 30000ms, got %d", err.RetryAfterMs())
		}
	})
}

func TestModelPreferredEndpoint(t *testing.T) {
	tests := []struct {
		name               string
		supportedEndpoints []string
		expectedEndpoint   string
	}{
		{
			name:               "empty endpoints uses default",
			supportedEndpoints: nil,
			expectedEndpoint:   DefaultEndpoint,
		},
		{
			name:               "empty slice uses default",
			supportedEndpoints: []string{},
			expectedEndpoint:   DefaultEndpoint,
		},
		{
			name:               "single endpoint returns that endpoint",
			supportedEndpoints: []string{"/responses"},
			expectedEndpoint:   "/responses",
		},
		{
			name:               "multiple endpoints returns first",
			supportedEndpoints: []string{"/chat/completions", "/responses"},
			expectedEndpoint:   "/chat/completions",
		},
		{
			name:               "multiple endpoints different order",
			supportedEndpoints: []string{"/v1/messages", "/chat/completions"},
			expectedEndpoint:   "/v1/messages",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := Model{
				ID:                 "test-model",
				SupportedEndpoints: test.supportedEndpoints,
			}
			result := model.PreferredEndpoint()
			if result != test.expectedEndpoint {
				t.Errorf("expected %s, got %s", test.expectedEndpoint, result)
			}
		})
	}
}

func TestDefaultEndpointConstant(t *testing.T) {
	if DefaultEndpoint != "/chat/completions" {
		t.Errorf("expected DefaultEndpoint to be /chat/completions, got %s", DefaultEndpoint)
	}
}
