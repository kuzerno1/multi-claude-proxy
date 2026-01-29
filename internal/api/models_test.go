package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/internal/provider"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	name           string
	models         []string
	modelsResponse *types.ModelsResponse
	modelsError    error
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Models() []string { return m.models }

func (m *mockProvider) SupportsModel(model string) bool {
	for _, supported := range m.models {
		if supported == model {
			return true
		}
	}
	return false
}

func (m *mockProvider) SendMessage(ctx context.Context, req *types.AnthropicRequest) (*types.AnthropicResponse, error) {
	return nil, nil
}

func (m *mockProvider) SendMessageStream(ctx context.Context, req *types.AnthropicRequest) (<-chan types.StreamEvent, error) {
	return nil, nil
}

func (m *mockProvider) ListModels(ctx context.Context) (*types.ModelsResponse, error) {
	if m.modelsError != nil {
		return nil, m.modelsError
	}
	return m.modelsResponse, nil
}

func (m *mockProvider) GetStatus(ctx context.Context) (*types.ProviderStatus, error) {
	return nil, nil
}

func (m *mockProvider) GenerateImage(ctx context.Context, req *types.ImageGenerationRequest) (*types.ImageGenerationResponse, error) {
	return nil, nil
}

func (m *mockProvider) Initialize(ctx context.Context) error { return nil }

func (m *mockProvider) Shutdown(ctx context.Context) error { return nil }

// AnthropicModelsResponse represents the expected Anthropic API response format.
type AnthropicModelsResponse struct {
	Data    []AnthropicModelInfo `json:"data"`
	FirstID string               `json:"first_id"`
	HasMore bool                 `json:"has_more"`
	LastID  string               `json:"last_id"`
}

// AnthropicModelInfo represents a model in the Anthropic format.
type AnthropicModelInfo struct {
	ID          string  `json:"id"`
	CreatedAt   *string `json:"created_at"` // nullable
	DisplayName string  `json:"display_name"`
	Type        string  `json:"type"`
}

func TestHandleModels_AnthropicFormat(t *testing.T) {
	t.Run("returns Anthropic-compatible response format", func(t *testing.T) {
		registry := provider.NewRegistry()
		createdAt := "2024-10-22T00:00:00Z"
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"claude-sonnet-4-5-thinking", "claude-opus-4-5-thinking"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{
						ID:          "claude-sonnet-4-5-thinking",
						DisplayName: "Claude Sonnet 4.5 (Thinking)",
						CreatedAt:   &createdAt,
						Type:        "model",
					},
					{
						ID:          "claude-opus-4-5-thinking",
						DisplayName: "Claude Opus 4.5 (Thinking)",
						CreatedAt:   &createdAt,
						Type:        "model",
					},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Verify response structure
		if len(resp.Data) != 2 {
			t.Fatalf("expected 2 models, got %d", len(resp.Data))
		}

		// Check first_id and last_id (sorted alphabetically: opus comes before sonnet)
		if resp.FirstID != "antigravity/claude-opus-4-5-thinking" {
			t.Errorf("expected first_id to be 'antigravity/claude-opus-4-5-thinking', got %s", resp.FirstID)
		}
		if resp.LastID != "antigravity/claude-sonnet-4-5-thinking" {
			t.Errorf("expected last_id to be 'antigravity/claude-sonnet-4-5-thinking', got %s", resp.LastID)
		}

		// Verify has_more is false when no pagination
		if resp.HasMore {
			t.Error("expected has_more to be false")
		}

		// Verify model structure
		for _, model := range resp.Data {
			if model.Type != "model" {
				t.Errorf("expected type 'model', got %s", model.Type)
			}
			if model.DisplayName == "" {
				t.Error("expected display_name to be set")
			}
			if model.ID == "" {
				t.Error("expected id to be set")
			}
		}
	})

	t.Run("returns null created_at when provider doesn't provide it", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"claude-sonnet-4-5"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{
						ID:          "claude-sonnet-4-5",
						DisplayName: "Claude Sonnet 4.5",
						CreatedAt:   nil, // No created_at from provider
						Type:        "model",
					},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		// Parse raw JSON to check for null
		var rawResp map[string]interface{}
		if err := json.NewDecoder(rr.Body).Decode(&rawResp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		data, ok := rawResp["data"].([]interface{})
		if !ok || len(data) == 0 {
			t.Fatal("expected data array with models")
		}

		model := data[0].(map[string]interface{})
		if model["created_at"] != nil {
			t.Errorf("expected created_at to be null, got %v", model["created_at"])
		}
	})

	t.Run("prefixes model IDs with provider name", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"claude-sonnet-4-5"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{
						ID:          "claude-sonnet-4-5",
						DisplayName: "Claude Sonnet 4.5",
						Type:        "model",
					},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(resp.Data) == 0 {
			t.Fatal("expected at least one model")
		}

		// Model ID should be prefixed with provider name
		expectedID := "antigravity/claude-sonnet-4-5"
		if resp.Data[0].ID != expectedID {
			t.Errorf("expected model ID '%s', got '%s'", expectedID, resp.Data[0].ID)
		}
	})
}

func TestHandleModels_Pagination(t *testing.T) {
	t.Run("respects limit parameter", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1", "model-2", "model-3", "model-4", "model-5"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
					{ID: "model-2", DisplayName: "Model 2", Type: "model"},
					{ID: "model-3", DisplayName: "Model 3", Type: "model"},
					{ID: "model-4", DisplayName: "Model 4", Type: "model"},
					{ID: "model-5", DisplayName: "Model 5", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models?limit=2", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(resp.Data) != 2 {
			t.Errorf("expected 2 models with limit=2, got %d", len(resp.Data))
		}

		if !resp.HasMore {
			t.Error("expected has_more to be true when more results exist")
		}
	})

	t.Run("supports after_id pagination", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1", "model-2", "model-3"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
					{ID: "model-2", DisplayName: "Model 2", Type: "model"},
					{ID: "model-3", DisplayName: "Model 3", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models?after_id=antigravity/model-1", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Should return models after model-1
		if len(resp.Data) != 2 {
			t.Errorf("expected 2 models after model-1, got %d", len(resp.Data))
		}

		if resp.FirstID != "antigravity/model-2" {
			t.Errorf("expected first_id to be 'antigravity/model-2', got %s", resp.FirstID)
		}
	})

	t.Run("supports before_id pagination", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1", "model-2", "model-3"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
					{ID: "model-2", DisplayName: "Model 2", Type: "model"},
					{ID: "model-3", DisplayName: "Model 3", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models?before_id=antigravity/model-3", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Should return models before model-3
		if len(resp.Data) != 2 {
			t.Errorf("expected 2 models before model-3, got %d", len(resp.Data))
		}

		if resp.LastID != "antigravity/model-2" {
			t.Errorf("expected last_id to be 'antigravity/model-2', got %s", resp.LastID)
		}
	})

	t.Run("supports after_id with limit combined", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1", "model-2", "model-3", "model-4", "model-5"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
					{ID: "model-2", DisplayName: "Model 2", Type: "model"},
					{ID: "model-3", DisplayName: "Model 3", Type: "model"},
					{ID: "model-4", DisplayName: "Model 4", Type: "model"},
					{ID: "model-5", DisplayName: "Model 5", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models?after_id=antigravity/model-1&limit=2", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Should return 2 models after model-1 (model-2 and model-3)
		if len(resp.Data) != 2 {
			t.Errorf("expected 2 models with after_id=model-1&limit=2, got %d", len(resp.Data))
		}

		if resp.FirstID != "antigravity/model-2" {
			t.Errorf("expected first_id to be 'antigravity/model-2', got %s", resp.FirstID)
		}
		if resp.LastID != "antigravity/model-3" {
			t.Errorf("expected last_id to be 'antigravity/model-3', got %s", resp.LastID)
		}

		// Should have more results since model-4 and model-5 exist
		if !resp.HasMore {
			t.Error("expected has_more to be true when more results exist after limit")
		}
	})

	t.Run("limit defaults to 20", func(t *testing.T) {
		registry := provider.NewRegistry()
		// Create 25 models
		models := make([]types.Model, 25)
		modelIDs := make([]string, 25)
		for i := 0; i < 25; i++ {
			modelIDs[i] = "model-" + string(rune('a'+i))
			models[i] = types.Model{
				ID:          modelIDs[i],
				DisplayName: "Model " + string(rune('A'+i)),
				Type:        "model",
			}
		}
		mockProv := &mockProvider{
			name:   "antigravity",
			models: modelIDs,
			modelsResponse: &types.ModelsResponse{
				Data: models,
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(resp.Data) != 20 {
			t.Errorf("expected default limit of 20 models, got %d", len(resp.Data))
		}

		if !resp.HasMore {
			t.Error("expected has_more to be true when more than 20 models exist")
		}
	})

	t.Run("limit max is 1000", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models?limit=2000", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		// Should not error, just cap at 1000
		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}
	})

	t.Run("limit min is 1", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1", "model-2"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
					{ID: "model-2", DisplayName: "Model 2", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models?limit=0", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Should use minimum limit of 1
		if len(resp.Data) < 1 {
			t.Error("expected at least 1 model with limit=0 (should use min of 1)")
		}
	})
}

func TestHandleModels_MultipleProviders(t *testing.T) {
	t.Run("merges models from multiple providers", func(t *testing.T) {
		registry := provider.NewRegistry()

		antigravityProv := &mockProvider{
			name:   "antigravity",
			models: []string{"claude-sonnet-4-5"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", Type: "model"},
				},
			},
		}
		registry.Register(antigravityProv)

		zaiProv := &mockProvider{
			name:   "zai",
			models: []string{"gemini-3-flash"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "gemini-3-flash", DisplayName: "Gemini 3 Flash", Type: "model"},
				},
			},
		}
		registry.Register(zaiProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(resp.Data) != 2 {
			t.Errorf("expected 2 models from 2 providers, got %d", len(resp.Data))
		}

		// Verify both providers are represented
		providersSeen := make(map[string]bool)
		for _, model := range resp.Data {
			if len(model.ID) > 0 {
				// Extract provider from ID (format: provider/model)
				for _, prefix := range []string{"antigravity/", "zai/"} {
					if len(model.ID) > len(prefix) && model.ID[:len(prefix)] == prefix {
						providersSeen[prefix[:len(prefix)-1]] = true
					}
				}
			}
		}

		if !providersSeen["antigravity"] {
			t.Error("expected antigravity provider models")
		}
		if !providersSeen["zai"] {
			t.Error("expected zai provider models")
		}
	})
}

func TestHandleModels_EdgeCases(t *testing.T) {
	t.Run("returns empty response when no models available", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(resp.Data) != 0 {
			t.Errorf("expected 0 models, got %d", len(resp.Data))
		}

		if resp.FirstID != "" {
			t.Errorf("expected empty first_id, got %s", resp.FirstID)
		}
		if resp.LastID != "" {
			t.Errorf("expected empty last_id, got %s", resp.LastID)
		}
		if resp.HasMore {
			t.Error("expected has_more to be false for empty list")
		}
	})

	t.Run("handles invalid after_id gracefully", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models?after_id=nonexistent-model", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		// Should return all models when after_id is not found
		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp AnthropicModelsResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Returns empty when cursor not found (after non-existent ID means nothing after)
		if len(resp.Data) != 0 {
			t.Errorf("expected 0 models when after_id not found, got %d", len(resp.Data))
		}
	})

	t.Run("returns 400 for invalid limit parameter", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models?limit=invalid", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rr.Code)
		}

		// Verify error response format
		var errResp struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}

		if errResp.Error.Type != "invalid_request_error" {
			t.Errorf("expected error type 'invalid_request_error', got '%s'", errResp.Error.Type)
		}
	})

	t.Run("returns 405 for non-GET requests", func(t *testing.T) {
		registry := provider.NewRegistry()
		server := NewServer(registry, nil)

		for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			req := httptest.NewRequest(method, "/v1/models", nil)
			rr := httptest.NewRecorder()

			server.handleModels(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("expected status 404 for %s, got %d", method, rr.Code)
			}
		}
	})
}

func TestHandleModels_ContentType(t *testing.T) {
	t.Run("returns application/json content type", func(t *testing.T) {
		registry := provider.NewRegistry()
		mockProv := &mockProvider{
			name:   "antigravity",
			models: []string{"model-1"},
			modelsResponse: &types.ModelsResponse{
				Data: []types.Model{
					{ID: "model-1", DisplayName: "Model 1", Type: "model"},
				},
			},
		}
		registry.Register(mockProv)

		server := NewServer(registry, nil)
		req := httptest.NewRequest("GET", "/v1/models", nil)
		rr := httptest.NewRecorder()

		server.handleModels(rr, req)

		contentType := rr.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("expected Content-Type 'application/json', got '%s'", contentType)
		}
	})
}
