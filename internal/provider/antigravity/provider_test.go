package antigravity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/internal/account"
)

func setupTestAccountManager(t *testing.T, accounts []account.Account) *account.Manager {
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	configPath := filepath.Join(tmpDir, "accounts.json")
	mgr := account.NewManager(configPath)

	for _, acc := range accounts {
		if err := mgr.AddAccount(acc); err != nil {
			t.Fatal(err)
		}
	}

	if err := mgr.Initialize(); err != nil {
		t.Fatal(err)
	}

	return mgr
}

func TestProvider_Initialize_FetchesModels(t *testing.T) {
	// Mock API server that returns available models
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"models": map[string]interface{}{
				"claude-sonnet-4-5-thinking": map[string]interface{}{
					"displayName": "Claude Sonnet 4.5 Thinking",
					"quotaInfo": map[string]interface{}{
						"remainingFraction": 0.85,
					},
				},
				"claude-opus-4-5-thinking": map[string]interface{}{
					"displayName": "Claude Opus 4.5 Thinking",
					"quotaInfo": map[string]interface{}{
						"remainingFraction": 0.75,
					},
				},
				"gemini-3-flash": map[string]interface{}{
					"displayName": "Gemini 3 Flash",
					"quotaInfo": map[string]interface{}{
						"remainingFraction": 1.0,
					},
				},
				// All models from API are supported (no filtering)
				"gpt-4": map[string]interface{}{
					"displayName": "GPT-4",
					"quotaInfo": map[string]interface{}{
						"remainingFraction": 1.0,
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Use manual source with APIKey - this bypasses OAuth token refresh
	accounts := []account.Account{
		{
			Email:    "test@example.com",
			Provider: "antigravity",
			Source:   "manual",
			APIKey:   "test-token",
		},
	}
	mgr := setupTestAccountManager(t, accounts)

	p := NewProvider(mgr, false)
	// Override client endpoints to use test server
	p.client.endpoints = []string{server.URL}

	err := p.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	models := p.Models()
	// Should have all 4 models from the API
	if len(models) != 4 {
		t.Errorf("expected 4 models, got %d: %v", len(models), models)
	}

	// Should support all fetched models
	if !p.SupportsModel("claude-sonnet-4-5-thinking") {
		t.Error("expected to support claude-sonnet-4-5-thinking")
	}
	if !p.SupportsModel("claude-opus-4-5-thinking") {
		t.Error("expected to support claude-opus-4-5-thinking")
	}
	if !p.SupportsModel("gemini-3-flash") {
		t.Error("expected to support gemini-3-flash")
	}
	if !p.SupportsModel("gpt-4") {
		t.Error("expected to support gpt-4")
	}
}

func TestProvider_Initialize_NoAccountsSkipsInit(t *testing.T) {
	mgr := account.NewManager("")

	p := NewProvider(mgr, false)

	err := p.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have no models when no accounts
	models := p.Models()
	if len(models) != 0 {
		t.Errorf("expected 0 models when no accounts, got %d", len(models))
	}
}

func TestProvider_Initialize_InvalidAccountSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"models": map[string]interface{}{
				"claude-sonnet-4-5": map[string]interface{}{
					"displayName": "Claude Sonnet 4.5",
					"quotaInfo": map[string]interface{}{
						"remainingFraction": 1.0,
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	accounts := []account.Account{
		{
			Email:     "invalid@example.com",
			Provider:  "antigravity",
			Source:    "manual",
			IsInvalid: true,
		},
		{
			Email:    "valid@example.com",
			Provider: "antigravity",
			Source:   "manual",
			APIKey:   "test-token",
		},
	}
	mgr := setupTestAccountManager(t, accounts)

	p := NewProvider(mgr, false)
	p.client.endpoints = []string{server.URL}

	err := p.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have successfully fetched models using the valid account
	models := p.Models()
	if len(models) != 1 {
		t.Errorf("expected 1 model, got %d", len(models))
	}

	if !p.SupportsModel("claude-sonnet-4-5") {
		t.Error("expected to support claude-sonnet-4-5")
	}
}

func TestProvider_Models_ThreadSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"models": map[string]interface{}{
				"claude-sonnet-4-5": map[string]interface{}{
					"displayName": "Claude Sonnet 4.5",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	accounts := []account.Account{
		{
			Email:    "test@example.com",
			Provider: "antigravity",
			Source:   "manual",
			APIKey:   "test-token",
		},
	}
	mgr := setupTestAccountManager(t, accounts)

	p := NewProvider(mgr, false)
	p.client.endpoints = []string{server.URL}

	_ = p.Initialize(context.Background())

	// Concurrent reads should not race
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_ = p.Models()
			_ = p.SupportsModel("claude-sonnet-4-5")
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
