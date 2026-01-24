package zai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
)

func TestFetchQuota(t *testing.T) {
	t.Run("successful quota fetch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(config.ZAIAuthHeader) != "Bearer api-key-123" {
				t.Errorf("expected header %s: Bearer api-key-123, got %s", config.ZAIAuthHeader, r.Header.Get(config.ZAIAuthHeader))
			}

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
							CurrentValue: 250,
							Remaining:    750,
							Percentage:   25,
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewClient()
		client.quotaURL = server.URL

		info, err := client.FetchQuota(context.Background(), "api-key-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info == nil {
			t.Fatal("expected info, got nil")
		}
		if info.MaxTokens != 1000 {
			t.Errorf("expected MaxTokens 1000, got %d", info.MaxTokens)
		}
		if info.UsedTokens != 250 {
			t.Errorf("expected UsedTokens 250, got %d", info.UsedTokens)
		}
		if info.RemainingTokens != 750 {
			t.Errorf("expected RemainingTokens 750, got %d", info.RemainingTokens)
		}
		if info.UsedPercentage != 25 {
			t.Errorf("expected UsedPercentage 25, got %d", info.UsedPercentage)
		}
		if info.RemainingPercentage != 75 {
			t.Errorf("expected RemainingPercentage 75, got %d", info.RemainingPercentage)
		}
		if info.RemainingFraction != 0.75 {
			t.Errorf("expected RemainingFraction 0.75, got %f", info.RemainingFraction)
		}
	})

	t.Run("quota fetch with nextResetTime", func(t *testing.T) {
		// Expected reset time: 1769036370828 milliseconds (from user's example)
		expectedResetTimeMs := int64(1769036370828)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate response when tokens have been used (currentValue > 0)
			// nextResetTime is only present when tokens have been consumed
			resp := map[string]interface{}{
				"code":    200,
				"msg":     "Operation successful",
				"success": true,
				"data": map[string]interface{}{
					"limits": []map[string]interface{}{
						{
							"type":          "TOKENS_LIMIT",
							"unit":          3,
							"number":        5,
							"usage":         200000000,
							"currentValue":  80626,
							"remaining":     199919374,
							"percentage":    1,
							"nextResetTime": expectedResetTimeMs,
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewClient()
		client.quotaURL = server.URL

		info, err := client.FetchQuota(context.Background(), "api-key-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info == nil {
			t.Fatal("expected info, got nil")
		}
		if info.ResetTime == nil {
			t.Fatal("expected ResetTime to be set, got nil")
		}
		if info.ResetTimeMs != expectedResetTimeMs {
			t.Errorf("expected ResetTimeMs %d, got %d", expectedResetTimeMs, info.ResetTimeMs)
		}
		// Verify the parsed time is correct
		expectedTime := time.UnixMilli(expectedResetTimeMs)
		if !info.ResetTime.Equal(expectedTime) {
			t.Errorf("expected ResetTime %v, got %v", expectedTime, *info.ResetTime)
		}
	})

	t.Run("quota fetch without nextResetTime (no usage)", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate response when no tokens have been used (currentValue = 0)
			// nextResetTime is NOT present in this case
			resp := map[string]interface{}{
				"code":    200,
				"msg":     "Operation successful",
				"success": true,
				"data": map[string]interface{}{
					"limits": []map[string]interface{}{
						{
							"type":         "TOKENS_LIMIT",
							"unit":         3,
							"number":       5,
							"usage":        200000000,
							"currentValue": 0,
							"remaining":    200000000,
							"percentage":   0,
							// Note: no nextResetTime field
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewClient()
		client.quotaURL = server.URL

		info, err := client.FetchQuota(context.Background(), "api-key-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info == nil {
			t.Fatal("expected info, got nil")
		}
		// ResetTime should be nil when no tokens have been used
		if info.ResetTime != nil {
			t.Errorf("expected ResetTime to be nil when no usage, got %v", info.ResetTime)
		}
		if info.ResetTimeMs != 0 {
			t.Errorf("expected ResetTimeMs to be 0 when no usage, got %d", info.ResetTimeMs)
		}
	})

	t.Run("auth error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("unauthorized"))
		}))
		defer server.Close()

		client := NewClient()
		client.quotaURL = server.URL

		info, err := client.FetchQuota(context.Background(), "wrong-key")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "authentication_error") {
			t.Errorf("expected authentication_error, got %v", err)
		}
		if info != nil {
			t.Errorf("expected nil info, got %v", info)
		}
	})

	t.Run("missing TOKENS_LIMIT", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := QuotaResponse{
				Code:    200,
				Success: true,
				Data: struct {
					Limits []QuotaLimit `json:"limits"`
				}{
					Limits: []QuotaLimit{
						{Type: "OTHER_LIMIT"},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewClient()
		client.quotaURL = server.URL

		info, err := client.FetchQuota(context.Background(), "api-key-123")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "TOKENS_LIMIT not found") {
			t.Errorf("expected 'TOKENS_LIMIT not found', got %v", err)
		}
		if info != nil {
			t.Errorf("expected nil info, got %v", info)
		}
	})
}

func TestVerifyAPIKey(t *testing.T) {
	t.Run("valid key", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := ModelsResponse{
				Object: "list",
				Data: []ModelEntry{
					{ID: "model-1"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewClient()
		client.baseURL = server.URL
		client.modelsPath = "" // So it calls just baseURL

		err := client.VerifyAPIKey(context.Background(), "valid-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		client := NewClient()
		client.baseURL = server.URL
		client.modelsPath = ""

		err := client.VerifyAPIKey(context.Background(), "invalid-key")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "authentication_error") {
			t.Errorf("expected authentication_error, got %v", err)
		}
	})
}
