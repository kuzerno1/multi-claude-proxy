package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
)

func TestParseImageGenerationRequest(t *testing.T) {
	t.Run("valid request with all fields", func(t *testing.T) {
		body := []byte(`{
			"prompt": "a sunset over mountains",
			"model": "gemini-3-pro-image",
			"aspect_ratio": "16:9",
			"count": 2,
			"session_id": "session-123"
		}`)

		req, err := parseImageGenerationRequest(body)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Prompt != "a sunset over mountains" {
			t.Errorf("expected prompt 'a sunset over mountains', got %s", req.Prompt)
		}
		if req.Model != "gemini-3-pro-image" {
			t.Errorf("expected model 'gemini-3-pro-image', got %s", req.Model)
		}
		if req.AspectRatio != "16:9" {
			t.Errorf("expected aspect_ratio '16:9', got %s", req.AspectRatio)
		}
		if req.Count != 2 {
			t.Errorf("expected count 2, got %d", req.Count)
		}
		if req.SessionID != "session-123" {
			t.Errorf("expected session_id 'session-123', got %s", req.SessionID)
		}
	})

	t.Run("applies default model", func(t *testing.T) {
		body := []byte(`{"prompt": "a cat"}`)

		req, err := parseImageGenerationRequest(body)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Model != config.DefaultImageModel {
			t.Errorf("expected default model %s, got %s", config.DefaultImageModel, req.Model)
		}
	})

	t.Run("applies default count when zero", func(t *testing.T) {
		body := []byte(`{"prompt": "a cat", "count": 0}`)

		req, err := parseImageGenerationRequest(body)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Count != config.DefaultImageCount {
			t.Errorf("expected default count %d, got %d", config.DefaultImageCount, req.Count)
		}
	})

	t.Run("applies default count when negative", func(t *testing.T) {
		body := []byte(`{"prompt": "a cat", "count": -1}`)

		req, err := parseImageGenerationRequest(body)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Count != config.DefaultImageCount {
			t.Errorf("expected default count %d, got %d", config.DefaultImageCount, req.Count)
		}
	})

	t.Run("caps count at max", func(t *testing.T) {
		body := []byte(`{"prompt": "a cat", "count": 100}`)

		req, err := parseImageGenerationRequest(body)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Count != config.MaxImageCount {
			t.Errorf("expected max count %d, got %d", config.MaxImageCount, req.Count)
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		body := []byte(`{invalid json}`)

		_, err := parseImageGenerationRequest(body)

		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestHandleImageGenerate_Validation(t *testing.T) {
	t.Run("returns 404 for non-POST methods", func(t *testing.T) {
		server := NewServer(nil, nil)

		methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}
		for _, method := range methods {
			req := httptest.NewRequest(method, "/v1/images/generate", nil)
			rr := httptest.NewRecorder()

			server.handleImageGenerate(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("expected 404 for %s, got %d", method, rr.Code)
			}
		}
	})

	t.Run("returns 400 for invalid JSON", func(t *testing.T) {
		server := NewServer(nil, nil)

		body := strings.NewReader(`{invalid json}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/images/generate", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		server.handleImageGenerate(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}

		var resp map[string]interface{}
		json.Unmarshal(rr.Body.Bytes(), &resp)
		errDetail := resp["error"].(map[string]interface{})
		if errDetail["type"] != "invalid_request_error" {
			t.Errorf("expected error type 'invalid_request_error', got %v", errDetail["type"])
		}
	})

	t.Run("returns 400 for missing prompt", func(t *testing.T) {
		server := NewServer(nil, nil)

		body := strings.NewReader(`{"model": "gemini-3-pro-image"}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/images/generate", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		server.handleImageGenerate(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}

		var resp map[string]interface{}
		json.Unmarshal(rr.Body.Bytes(), &resp)
		errDetail := resp["error"].(map[string]interface{})
		if !strings.Contains(errDetail["message"].(string), "prompt is required") {
			t.Errorf("expected 'prompt is required' error, got %v", errDetail["message"])
		}
	})

	t.Run("returns 400 for empty prompt", func(t *testing.T) {
		server := NewServer(nil, nil)

		body := strings.NewReader(`{"prompt": ""}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/images/generate", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		server.handleImageGenerate(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("returns 500 when no registry configured", func(t *testing.T) {
		server := NewServer(nil, nil)

		body := strings.NewReader(`{"prompt": "a cat"}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/images/generate", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		server.handleImageGenerate(rr, req)

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", rr.Code)
		}
	})
}
