package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
)

// errInvalidAuthFormat indicates the Authorization header is present but not in Bearer format.
var errInvalidAuthFormat = errors.New("invalid authorization header format")

// APIKeyAuth validates API key authentication.
// Supports:
//   - Header: x-api-key: <key>
//   - Header: Authorization: Bearer <key>
//
// Health endpoint (/health) is exempt from authentication.
// Returns 500 Internal Server Error if PROXY_API_KEY is not configured.
func APIKeyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health endpoint is exempt from authentication
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		expectedKey := config.GetProxyAPIKey()

		// Check for server misconfiguration - API key must be set
		if expectedKey == "" {
			writeServerError(w, "Server misconfigured: PROXY_API_KEY not set")
			return
		}

		// Extract API key from request
		apiKey, err := extractAPIKey(r)
		if err != nil {
			if errors.Is(err, errInvalidAuthFormat) {
				writeAuthError(w, "Invalid Authorization header format")
				return
			}
			writeAuthError(w, "Missing API key")
			return
		}

		if apiKey == "" {
			writeAuthError(w, "Missing API key")
			return
		}

		// Validate API key using constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(apiKey), []byte(expectedKey)) != 1 {
			writeAuthError(w, "Invalid API key")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractAPIKey extracts the API key from the request headers.
// Returns the API key and nil error if found.
// Returns empty string and nil error if no key found.
// Returns empty string and errInvalidAuthFormat if Authorization header is present but not Bearer format.
func extractAPIKey(r *http.Request) (string, error) {
	// Check x-api-key header first (Anthropic standard)
	if key := r.Header.Get("x-api-key"); key != "" {
		return key, nil
	}

	// Check Authorization header (Bearer token)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			return strings.TrimPrefix(authHeader, "Bearer "), nil
		}
		// Authorization header present but not Bearer format
		return "", errInvalidAuthFormat
	}

	return "", nil
}

// authErrorResponse represents an Anthropic-compatible authentication error.
type authErrorResponse struct {
	Type  string         `json:"type"`
	Error authErrorDetail `json:"error"`
}

type authErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// writeAuthError writes an Anthropic-compatible authentication error response.
func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	resp := authErrorResponse{
		Type: "error",
		Error: authErrorDetail{
			Type:    "authentication_error",
			Message: message,
		},
	}
	// Error handling: if encoding fails, response is already on wire - nothing more to do
	_ = json.NewEncoder(w).Encode(resp)
}

// writeServerError writes an Anthropic-compatible server error response for misconfigurations.
func writeServerError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	resp := authErrorResponse{
		Type: "error",
		Error: authErrorDetail{
			Type:    "api_error",
			Message: message,
		},
	}
	// Error handling: if encoding fails, response is already on wire - nothing more to do
	_ = json.NewEncoder(w).Encode(resp)
}
