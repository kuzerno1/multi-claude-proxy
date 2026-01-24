// Package api provides HTTP server components for the proxy.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSEWriter wraps http.ResponseWriter to provide SSE streaming capabilities.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter creates a new SSE writer and configures the response for streaming.
// Returns an error if the response writer doesn't support flushing.
func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Send initial headers
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	return &SSEWriter{w: w, flusher: flusher}, nil
}

// WriteEvent writes an SSE event with the given event type and data.
func (s *SSEWriter) WriteEvent(eventType string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal event data: %w", err)
	}

	// Format: event: <type>\ndata: <json>\n\n
	_, err = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", eventType, jsonData)
	if err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}

	s.flusher.Flush()
	return nil
}

// WriteData writes an SSE data-only event (no event type).
func (s *SSEWriter) WriteData(data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	_, err = fmt.Fprintf(s.w, "data: %s\n\n", jsonData)
	if err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	s.flusher.Flush()
	return nil
}

// WriteRaw writes raw SSE data without JSON marshaling.
func (s *SSEWriter) WriteRaw(eventType string, rawJSON []byte) error {
	_, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", eventType, rawJSON)
	if err != nil {
		return fmt.Errorf("failed to write raw event: %w", err)
	}

	s.flusher.Flush()
	return nil
}

// Flush manually flushes the response.
func (s *SSEWriter) Flush() {
	s.flusher.Flush()
}

// WriteError writes an SSE error event (Node parity).
// This is used when an error occurs after headers have been sent.
func (s *SSEWriter) WriteError(errorType, message string) error {
	errorEvent := map[string]interface{}{
		"type": "error",
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	}
	return s.WriteEvent("error", errorEvent)
}
