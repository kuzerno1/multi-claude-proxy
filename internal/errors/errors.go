// Package errors provides custom error types for the proxy.
package errors

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// ErrorType represents the type of error in Anthropic format.
type ErrorType string

const (
	ErrorTypeInvalidRequest ErrorType = "invalid_request_error"
	ErrorTypeAuthentication ErrorType = "authentication_error"
	ErrorTypePermission     ErrorType = "permission_error"
	ErrorTypeNotFound       ErrorType = "not_found_error"
	ErrorTypeRateLimit      ErrorType = "rate_limit_error"
	ErrorTypeAPI            ErrorType = "api_error"
	ErrorTypeOverloaded     ErrorType = "overloaded_error"
)

// AnthropicError represents an error response in Anthropic format.
type AnthropicError struct {
	Type   string      `json:"type"` // Always "error"
	Detail ErrorDetail `json:"error"`
	// HTTPStatus overrides the default status code mapping when set (Node parity).
	HTTPStatus int `json:"-"`
}

// ErrorDetail contains error details.
type ErrorDetail struct {
	Type    ErrorType `json:"type"`
	Message string    `json:"message"`
}

// Error implements the error interface.
func (e *AnthropicError) Error() string {
	return e.Detail.Message
}

// ToJSON returns the error as a JSON byte slice.
func (e *AnthropicError) ToJSON() []byte {
	data, _ := json.Marshal(e)
	return data
}

// StatusCode returns the HTTP status code for this error.
func (e *AnthropicError) StatusCode() int {
	if e.HTTPStatus != 0 {
		return e.HTTPStatus
	}

	switch e.Detail.Type {
	case ErrorTypeInvalidRequest:
		return http.StatusBadRequest
	case ErrorTypeAuthentication:
		return http.StatusUnauthorized
	case ErrorTypePermission:
		return http.StatusForbidden
	case ErrorTypeNotFound:
		return http.StatusNotFound
	case ErrorTypeRateLimit:
		return http.StatusTooManyRequests
	case ErrorTypeOverloaded:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// NewError creates a new AnthropicError.
func NewError(errType ErrorType, message string) *AnthropicError {
	return &AnthropicError{
		Type: "error",
		Detail: ErrorDetail{
			Type:    errType,
			Message: message,
		},
	}
}

// InvalidRequest creates an invalid request error.
func InvalidRequest(message string) *AnthropicError {
	return NewError(ErrorTypeInvalidRequest, message)
}

// AuthenticationError creates an authentication error.
func AuthenticationError(message string) *AnthropicError {
	return NewError(ErrorTypeAuthentication, message)
}

// RateLimitError creates a rate limit error.
func RateLimitError(message string) *AnthropicError {
	return NewError(ErrorTypeRateLimit, message)
}

// APIError creates an API error.
func APIError(message string) *AnthropicError {
	return NewError(ErrorTypeAPI, message)
}

// OverloadedError creates an overloaded error.
func OverloadedError(message string) *AnthropicError {
	return NewError(ErrorTypeOverloaded, message)
}

// FromError converts a Go error to an AnthropicError.
func FromError(err error) *AnthropicError {
	if err == nil {
		return nil
	}

	// Check if it's already an AnthropicError
	if ae, ok := err.(*AnthropicError); ok {
		return ae
	}

	errStr := err.Error()

	// Node parity: match src/server.js parseError() ordering.

	// Auth errors
	if strings.Contains(errStr, "401") || strings.Contains(errStr, "UNAUTHENTICATED") {
		return AuthenticationError("Authentication failed. Make sure Antigravity is running with a valid token.")
	}

	// Rate limit / quota exhaustion errors
	if strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "RESOURCE_EXHAUSTED") ||
		strings.Contains(errStr, "QUOTA_EXHAUSTED") {
		return InvalidRequest(formatQuotaExhaustedMessage(errStr))
	}

	// Invalid request errors
	if strings.Contains(errStr, "invalid_request_error") || strings.Contains(errStr, "INVALID_ARGUMENT") {
		ae := InvalidRequest(errStr)
		msgRe := regexp.MustCompile(`"message":"([^"]+)"`)
		if matches := msgRe.FindStringSubmatch(errStr); len(matches) == 2 {
			ae.Detail.Message = matches[1]
		}
		return ae
	}

	// Upstream connectivity errors
	if strings.Contains(errStr, "All endpoints failed") {
		ae := APIError("Unable to connect to Claude API. Check that Antigravity is running.")
		ae.HTTPStatus = http.StatusServiceUnavailable
		return ae
	}

	// Permission errors
	if strings.Contains(errStr, "PERMISSION_DENIED") {
		return NewError(ErrorTypePermission, "Permission denied. Check your Antigravity license.")
	}

	lowerErr := strings.ToLower(errStr)

	// Authentication errors
	if strings.Contains(lowerErr, "auth") ||
		strings.Contains(lowerErr, "token") ||
		strings.Contains(lowerErr, "401") ||
		strings.Contains(lowerErr, "403") ||
		strings.Contains(lowerErr, "unauthenticated") {
		return AuthenticationError(errStr)
	}

	// Overloaded errors
	if strings.Contains(lowerErr, "overloaded") ||
		strings.Contains(lowerErr, "503") ||
		strings.Contains(lowerErr, "service unavailable") {
		return OverloadedError(errStr)
	}

	// Not found errors
	if strings.Contains(lowerErr, "not found") ||
		strings.Contains(lowerErr, "404") {
		return NewError(ErrorTypeNotFound, errStr)
	}

	// Invalid request errors
	if strings.Contains(lowerErr, "invalid") ||
		strings.Contains(lowerErr, "bad request") ||
		strings.Contains(lowerErr, "400") {
		return InvalidRequest(errStr)
	}

	// Default to API error
	return APIError(errStr)
}

func formatQuotaExhaustedMessage(errStr string) string {
	resetRe := regexp.MustCompile(`(?i)quota will reset after ([0-9hms]+)`)
	modelRe := regexp.MustCompile(`Rate limited on ([^.]+)\.`)
	modelJSONRe := regexp.MustCompile(`"model":\s*"([^"]+)"`)

	reset := ""
	if matches := resetRe.FindStringSubmatch(errStr); len(matches) == 2 {
		reset = matches[1]
	}

	model := "the model"
	if matches := modelRe.FindStringSubmatch(errStr); len(matches) == 2 {
		model = matches[1]
	} else if matches := modelJSONRe.FindStringSubmatch(errStr); len(matches) == 2 {
		model = matches[1]
	}

	if reset != "" {
		return fmt.Sprintf("You have exhausted your capacity on %s. Quota will reset after %s.", model, reset)
	}
	return fmt.Sprintf("You have exhausted your capacity on %s. Please wait for your quota to reset.", model)
}

// IsRateLimitError returns true if the error is a rate limit error.
func IsRateLimitError(err error) bool {
	if ae, ok := err.(*AnthropicError); ok {
		return ae.Detail.Type == ErrorTypeRateLimit
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "quota") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "resource_exhausted")
}

// IsAuthError returns true if the error is an authentication error.
func IsAuthError(err error) bool {
	if ae, ok := err.(*AnthropicError); ok {
		return ae.Detail.Type == ErrorTypeAuthentication
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "auth") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403")
}

// Wrap wraps an error with additional context.
func Wrap(err error, context string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}
