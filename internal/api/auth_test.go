package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAPIKeyAuth(t *testing.T) {
	// Save and restore env var
	originalKey := os.Getenv("PROXY_API_KEY")
	defer func() {
		if originalKey != "" {
			os.Setenv("PROXY_API_KEY", originalKey)
		} else {
			os.Unsetenv("PROXY_API_KEY")
		}
	}()

	validKey := "test-api-key-12345"
	os.Setenv("PROXY_API_KEY", validKey)

	// Create a simple handler for testing
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	authMiddleware := APIKeyAuth(nextHandler)

	tests := []struct {
		name           string
		headerName     string
		headerValue    string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "valid x-api-key header",
			headerName:     "x-api-key",
			headerValue:    validKey,
			expectedStatus: http.StatusOK,
			expectedBody:   "OK",
		},
		{
			name:           "valid X-API-Key header (case insensitive)",
			headerName:     "X-API-Key",
			headerValue:    validKey,
			expectedStatus: http.StatusOK,
			expectedBody:   "OK",
		},
		{
			name:           "valid Authorization Bearer header",
			headerName:     "Authorization",
			headerValue:    "Bearer " + validKey,
			expectedStatus: http.StatusOK,
			expectedBody:   "OK",
		},
		{
			name:           "missing API key",
			headerName:     "",
			headerValue:    "",
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   `{"type":"error","error":{"type":"authentication_error","message":"Missing API key"}}`,
		},
		{
			name:           "invalid API key",
			headerName:     "x-api-key",
			headerValue:    "wrong-key",
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   `{"type":"error","error":{"type":"authentication_error","message":"Invalid API key"}}`,
		},
		{
			name:           "invalid Authorization format",
			headerName:     "Authorization",
			headerValue:    "Basic some-value",
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   `{"type":"error","error":{"type":"authentication_error","message":"Invalid Authorization header format"}}`,
		},
		{
			name:           "Authorization without Bearer prefix",
			headerName:     "Authorization",
			headerValue:    validKey,
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   `{"type":"error","error":{"type":"authentication_error","message":"Invalid Authorization header format"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
			if tt.headerName != "" && tt.headerValue != "" {
				req.Header.Set(tt.headerName, tt.headerValue)
			}

			rr := httptest.NewRecorder()
			authMiddleware.ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.expectedStatus)
			}

			// For error responses, compare parsed JSON to handle whitespace/formatting differences
			if tt.expectedStatus == http.StatusUnauthorized {
				var gotResp, wantResp authErrorResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &gotResp); err != nil {
					t.Fatalf("failed to parse response body: %v", err)
				}
				if err := json.Unmarshal([]byte(tt.expectedBody), &wantResp); err != nil {
					t.Fatalf("failed to parse expected body: %v", err)
				}
				if gotResp != wantResp {
					t.Errorf("response = %+v, want %+v", gotResp, wantResp)
				}
			} else if rr.Body.String() != tt.expectedBody {
				t.Errorf("body = %q, want %q", rr.Body.String(), tt.expectedBody)
			}
		})
	}
}

func TestAPIKeyAuth_HealthEndpointBypassed(t *testing.T) {
	// Save and restore env var
	originalKey := os.Getenv("PROXY_API_KEY")
	defer func() {
		if originalKey != "" {
			os.Setenv("PROXY_API_KEY", originalKey)
		} else {
			os.Unsetenv("PROXY_API_KEY")
		}
	}()

	os.Setenv("PROXY_API_KEY", "some-key")

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("healthy"))
	})

	authMiddleware := APIKeyAuth(nextHandler)

	// Request to /health without API key should succeed
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	authMiddleware.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("health endpoint status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestAPIKeyAuth_ContentTypeJSON(t *testing.T) {
	// Save and restore env var
	originalKey := os.Getenv("PROXY_API_KEY")
	defer func() {
		if originalKey != "" {
			os.Setenv("PROXY_API_KEY", originalKey)
		} else {
			os.Unsetenv("PROXY_API_KEY")
		}
	}()

	os.Setenv("PROXY_API_KEY", "some-key")

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	authMiddleware := APIKeyAuth(nextHandler)

	// Request without API key should return JSON content type
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	rr := httptest.NewRecorder()

	authMiddleware.ServeHTTP(rr, req)

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

func TestAPIKeyAuth_EmptyAPIKeyConfigured(t *testing.T) {
	// Save and restore env var
	originalKey := os.Getenv("PROXY_API_KEY")
	defer func() {
		if originalKey != "" {
			os.Setenv("PROXY_API_KEY", originalKey)
		} else {
			os.Unsetenv("PROXY_API_KEY")
		}
	}()

	// Simulate misconfigured server - no API key set
	os.Unsetenv("PROXY_API_KEY")

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	authMiddleware := APIKeyAuth(nextHandler)

	// Request with any API key should be rejected when server has no key configured
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	req.Header.Set("x-api-key", "any-key")
	rr := httptest.NewRecorder()

	authMiddleware.ServeHTTP(rr, req)

	// Should return 500 Internal Server Error for misconfigured server
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	// Verify error message indicates misconfiguration
	var resp authErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}

	expectedType := "api_error"
	if resp.Error.Type != expectedType {
		t.Errorf("error type = %q, want %q", resp.Error.Type, expectedType)
	}
}
