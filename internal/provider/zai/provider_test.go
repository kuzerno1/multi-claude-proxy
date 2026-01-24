package zai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/internal/account"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
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

func TestProvider_Initialize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ModelsResponse{
			Object: "list",
			Data: []ModelEntry{
				{ID: "zai/model-1"},
				{ID: "zai/model-2"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	accounts := []account.Account{
		{
			Email:    "test@example.com",
			Provider: "zai",
			Source:   "manual",
			APIKey:   "test-key",
		},
	}
	mgr := setupTestAccountManager(t, accounts)

	p := NewProvider(mgr)
	p.client.baseURL = server.URL
	p.client.modelsPath = ""

	err := p.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	models := p.Models()
	if len(models) != 2 {
		t.Errorf("expected 2 models, got %d", len(models))
	}

	if !p.SupportsModel("zai/model-1") {
		t.Error("expected to support zai/model-1")
	}
}

func TestProvider_SendMessage_Failover(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First account fails with 429
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": {"type": "rate_limit_error", "message": "too many requests"}}`))
			return
		}

		// Second account succeeds
		resp := types.AnthropicResponse{
			ID:    "msg_123",
			Type:  "message",
			Role:  "assistant",
			Model: "zai/model-1",
			Content: []types.ContentBlock{
				{Type: "text", Text: "Hello from second account"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	accounts := []account.Account{
		{
			Email:    "acc1@example.com",
			Provider: "zai",
			Source:   "manual",
			APIKey:   "key1",
		},
		{
			Email:    "acc2@example.com",
			Provider: "zai",
			Source:   "manual",
			APIKey:   "key2",
		},
	}
	mgr := setupTestAccountManager(t, accounts)

	p := NewProvider(mgr)
	p.client.baseURL = server.URL
	p.client.modelsPath = ""

	req := &types.AnthropicRequest{
		Model: "zai/model-1",
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
		},
	}

	resp, err := p.SendMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}

	if resp.ID != "msg_123" {
		t.Errorf("expected msg_123, got %s", resp.ID)
	}
}

func TestProvider_GetStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := QuotaResponse{
			Code:    200,
			Success: true,
			Data: struct {
				Limits []QuotaLimit `json:"limits"`
			}{
				Limits: []QuotaLimit{
					{
						Type:         "TOKENS_LIMIT",
						Usage:        1000,
						CurrentValue: 100,
						Remaining:    900,
						Percentage:   10,
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	accounts := []account.Account{
		{
			Email:    "test@example.com",
			Provider: "zai",
			Source:   "manual",
			APIKey:   "test-key",
		},
	}
	mgr := setupTestAccountManager(t, accounts)

	p := NewProvider(mgr)
	p.client.quotaURL = server.URL
	p.models = []string{"zai/model-1"}
	p.modelSet = map[string]bool{"zai/model-1": true}

	status, err := p.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.Name != "zai" {
		t.Errorf("expected name zai, got %s", status.Name)
	}

	if len(status.Accounts) != 1 {
		t.Fatalf("expected 1 account status, got %d", len(status.Accounts))
	}

	accStatus := status.Accounts[0]
	if accStatus.Email != "test@example.com" {
		t.Errorf("expected test@example.com, got %s", accStatus.Email)
	}

	quota, ok := accStatus.Limits["zai/model-1"]
	if !ok {
		t.Fatal("expected quota for zai/model-1")
	}

	if quota.RemainingPercentage != 90 {
		t.Errorf("expected 90%% remaining, got %d%%", quota.RemainingPercentage)
	}
}
