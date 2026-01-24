package api

import (
	"strings"
	"testing"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/account"
)

func TestRenderAccountLimitsJSON(t *testing.T) {
	t.Run("formats account limits correctly", func(t *testing.T) {
		sortedModels := []string{"antigravity/claude-sonnet-4-5", "antigravity/gemini-3-flash"}
		accountLimits := []map[string]interface{}{
			{
				"email":    "test@example.com",
				"provider": "antigravity",
				"status":   "ok",
				"error":    "",
				"models": map[string]interface{}{
					"antigravity/claude-sonnet-4-5": map[string]interface{}{
						"remainingFraction": 0.80,
						"resetTime":         "2025-01-01T00:00:00Z",
					},
				},
			},
		}

		result := renderAccountLimitsJSON(sortedModels, accountLimits)

		if len(result) != 1 {
			t.Fatalf("expected 1 account, got %d", len(result))
		}

		acc := result[0]
		if acc["email"] != "test@example.com" {
			t.Errorf("email = %v, want test@example.com", acc["email"])
		}
		if acc["status"] != "ok" {
			t.Errorf("status = %v, want ok", acc["status"])
		}

		limits, ok := acc["limits"].(map[string]interface{})
		if !ok {
			t.Fatal("limits is not a map")
		}

		modelLimit, ok := limits["antigravity/claude-sonnet-4-5"].(map[string]interface{})
		if !ok {
			t.Fatal("model limit is not a map")
		}

		if modelLimit["remaining"] != "80%" {
			t.Errorf("remaining = %v, want 80%%", modelLimit["remaining"])
		}
	})

	t.Run("sets error to nil for empty error string", func(t *testing.T) {
		sortedModels := []string{}
		accountLimits := []map[string]interface{}{
			{
				"email":    "test@example.com",
				"provider": "antigravity",
				"status":   "ok",
				"error":    "",
				"models":   map[string]interface{}{},
			},
		}

		result := renderAccountLimitsJSON(sortedModels, accountLimits)

		if result[0]["error"] != nil {
			t.Errorf("error = %v, want nil", result[0]["error"])
		}
	})

	t.Run("only shows models for matching provider", func(t *testing.T) {
		// Set up account limits with models from different providers
		accountLimits := []map[string]interface{}{
			{
				"email":    "zai-user@example.com",
				"provider": "zai",
				"status":   "ok",
				"models": map[string]interface{}{
					"zai/claude-sonnet-4-5": map[string]interface{}{
						"remainingFraction": 0.85,
						"resetTime":         nil,
					},
				},
			},
			{
				"email":    "ag-user@example.com",
				"provider": "antigravity",
				"status":   "ok",
				"models": map[string]interface{}{
					"antigravity/claude-sonnet-4-5-thinking": map[string]interface{}{
						"remainingFraction": 0.75,
						"resetTime":         "2026-01-22T10:00:00Z",
					},
				},
			},
		}

		// sortedModels contains ALL models from ALL providers
		sortedModels := []string{
			"antigravity/claude-sonnet-4-5-thinking",
			"zai/claude-sonnet-4-5",
		}

		result := renderAccountLimitsJSON(sortedModels, accountLimits)

		// Verify result length
		if len(result) != 2 {
			t.Fatalf("expected 2 accounts, got %d", len(result))
		}

		// Check first account (zai-user) - should only have zai models
		zaiAccount := result[0]
		zaiLimits, ok := zaiAccount["limits"].(map[string]interface{})
		if !ok {
			t.Fatal("expected limits to be map[string]interface{}")
		}

		// Z.AI account should NOT have antigravity models
		if _, hasAntigravity := zaiLimits["antigravity/claude-sonnet-4-5-thinking"]; hasAntigravity {
			t.Error("Z.AI account should not include antigravity models")
		}

		// Z.AI account SHOULD have zai models
		if _, hasZai := zaiLimits["zai/claude-sonnet-4-5"]; !hasZai {
			t.Error("Z.AI account should include zai models")
		}

		// Check second account (ag-user) - should only have antigravity models
		agAccount := result[1]
		agLimits, ok := agAccount["limits"].(map[string]interface{})
		if !ok {
			t.Fatal("expected limits to be map[string]interface{}")
		}

		// Antigravity account should NOT have zai models
		if _, hasZai := agLimits["zai/claude-sonnet-4-5"]; hasZai {
			t.Error("Antigravity account should not include Z.AI models")
		}

		// Antigravity account SHOULD have antigravity models
		if _, hasAntigravity := agLimits["antigravity/claude-sonnet-4-5-thinking"]; !hasAntigravity {
			t.Error("Antigravity account should include antigravity models")
		}
	})

	t.Run("handles accounts with no models", func(t *testing.T) {
		accountLimits := []map[string]interface{}{
			{
				"email":    "error-user@example.com",
				"provider": "zai",
				"status":   "error",
				"error":    "authentication error",
				"models":   map[string]interface{}{},
			},
		}

		sortedModels := []string{
			"antigravity/claude-sonnet-4-5-thinking",
			"zai/claude-sonnet-4-5",
		}

		result := renderAccountLimitsJSON(sortedModels, accountLimits)

		if len(result) != 1 {
			t.Fatalf("expected 1 account, got %d", len(result))
		}

		// Error account should have empty limits
		account := result[0]
		limits, ok := account["limits"].(map[string]interface{})
		if !ok {
			t.Fatal("expected limits to be map[string]interface{}")
		}

		if len(limits) != 0 {
			t.Errorf("expected empty limits for error account, got %d models", len(limits))
		}
	})
}

func TestRenderAccountLimitsTable(t *testing.T) {
	t.Run("generates table with header and summary", func(t *testing.T) {
		now := time.Date(2025, 1, 23, 12, 0, 0, 0, time.UTC)
		allAccounts := []account.Account{
			{
				Email:    "user1@example.com",
				Provider: "antigravity",
			},
		}
		accountLimits := []map[string]interface{}{
			{
				"email":    "user1@example.com",
				"provider": "antigravity",
				"status":   "ok",
				"models":   map[string]interface{}{},
			},
		}
		sortedModels := []string{"antigravity/claude-sonnet-4-5"}

		result := renderAccountLimitsTable(now, allAccounts, accountLimits, sortedModels)

		if !strings.Contains(result, "Account Limits") {
			t.Error("table should contain 'Account Limits' header")
		}
		if !strings.Contains(result, "1 total") {
			t.Error("table should contain account count")
		}
		if !strings.Contains(result, "user1") {
			t.Error("table should contain account email prefix")
		}
	})

	t.Run("handles non-float64 remainingFraction without panic", func(t *testing.T) {
		now := time.Date(2025, 1, 23, 12, 0, 0, 0, time.UTC)
		allAccounts := []account.Account{
			{
				Email:    "user1@example.com",
				Provider: "antigravity",
			},
		}
		// This tests the case where remainingFraction is an unexpected type
		// (e.g., string or int instead of float64)
		accountLimits := []map[string]interface{}{
			{
				"email":    "user1@example.com",
				"provider": "antigravity",
				"status":   "ok",
				"models": map[string]interface{}{
					"antigravity/claude-sonnet-4-5": map[string]interface{}{
						"remainingFraction": "not-a-number", // String instead of float64
						"resetTime":         nil,
					},
				},
			},
		}
		sortedModels := []string{"antigravity/claude-sonnet-4-5"}

		// Should not panic - gracefully handle unexpected type
		result := renderAccountLimitsTable(now, allAccounts, accountLimits, sortedModels)

		// Should show "-" for the invalid value
		if !strings.Contains(result, "-") {
			t.Error("table should show '-' for invalid remainingFraction type")
		}
	})

	t.Run("handles int remainingFraction without panic", func(t *testing.T) {
		now := time.Date(2025, 1, 23, 12, 0, 0, 0, time.UTC)
		allAccounts := []account.Account{
			{
				Email:    "user1@example.com",
				Provider: "antigravity",
			},
		}
		// Test with int instead of float64 (can happen with some JSON parsers)
		accountLimits := []map[string]interface{}{
			{
				"email":    "user1@example.com",
				"provider": "antigravity",
				"status":   "ok",
				"models": map[string]interface{}{
					"antigravity/claude-sonnet-4-5": map[string]interface{}{
						"remainingFraction": int(1), // int instead of float64
						"resetTime":         nil,
					},
				},
			},
		}
		sortedModels := []string{"antigravity/claude-sonnet-4-5"}

		// Should not panic
		result := renderAccountLimitsTable(now, allAccounts, accountLimits, sortedModels)

		// Just verify it doesn't panic and returns something
		if result == "" {
			t.Error("expected non-empty result")
		}
	})
}

func TestHelperFunctions(t *testing.T) {
	t.Run("padRight pads short strings", func(t *testing.T) {
		result := padRight("hi", 5)
		if result != "hi   " {
			t.Errorf("padRight = %q, want %q", result, "hi   ")
		}
	})

	t.Run("padRight returns long strings unchanged", func(t *testing.T) {
		result := padRight("hello world", 5)
		if result != "hello world" {
			t.Errorf("padRight = %q, want %q", result, "hello world")
		}
	})

	t.Run("formatLocaleTime formats RFC3339", func(t *testing.T) {
		result := formatLocaleTime("2025-01-23T12:00:00Z")
		if result == "Invalid Date" {
			t.Error("should parse valid RFC3339 date")
		}
	})

	t.Run("formatLocaleTime returns Invalid Date for bad input", func(t *testing.T) {
		result := formatLocaleTime("not-a-date")
		if result != "Invalid Date" {
			t.Errorf("result = %q, want %q", result, "Invalid Date")
		}
	})

	t.Run("formatISOTimeUTC formats time correctly", func(t *testing.T) {
		tm := time.Date(2025, 1, 23, 12, 30, 45, 123000000, time.UTC)
		result := formatISOTimeUTC(tm)
		expected := "2025-01-23T12:30:45.123Z"
		if result != expected {
			t.Errorf("result = %q, want %q", result, expected)
		}
	})

	t.Run("parseResetMs calculates milliseconds correctly", func(t *testing.T) {
		now := time.Date(2025, 1, 23, 12, 0, 0, 0, time.UTC)
		result := parseResetMs(now, "2025-01-23T12:01:00Z")
		expected := int64(60000) // 1 minute in ms
		if result != expected {
			t.Errorf("result = %d, want %d", result, expected)
		}
	})

	t.Run("parseResetMs returns 0 for invalid date", func(t *testing.T) {
		now := time.Now()
		result := parseResetMs(now, "invalid")
		if result != 0 {
			t.Errorf("result = %d, want 0", result)
		}
	})

	t.Run("formatDurationMs formats hours", func(t *testing.T) {
		result := formatDurationMs(3661000) // 1h 1m 1s
		if result != "1h1m1s" {
			t.Errorf("result = %q, want %q", result, "1h1m1s")
		}
	})

	t.Run("formatDurationMs formats minutes", func(t *testing.T) {
		result := formatDurationMs(61000) // 1m 1s
		if result != "1m1s" {
			t.Errorf("result = %q, want %q", result, "1m1s")
		}
	})

	t.Run("formatDurationMs formats seconds only", func(t *testing.T) {
		result := formatDurationMs(30000) // 30s
		if result != "30s" {
			t.Errorf("result = %q, want %q", result, "30s")
		}
	})

	t.Run("hasProviderPrefix matches correctly", func(t *testing.T) {
		if !hasProviderPrefix("antigravity/model", "antigravity") {
			t.Error("should match antigravity prefix")
		}
		if hasProviderPrefix("zai/model", "antigravity") {
			t.Error("should not match different prefix")
		}
		if hasProviderPrefix("", "antigravity") {
			t.Error("should not match empty modelID")
		}
		if hasProviderPrefix("antigravity/model", "") {
			t.Error("should not match empty provider")
		}
	})

	t.Run("filterModelsForProvider filters correctly", func(t *testing.T) {
		models := []string{
			"antigravity/claude-sonnet-4-5",
			"antigravity/gemini-3-flash",
			"zai/claude-sonnet-4-5",
		}
		result := filterModelsForProvider(models, "antigravity")

		if len(result) != 2 {
			t.Fatalf("expected 2 models, got %d", len(result))
		}
		for _, m := range result {
			if !strings.HasPrefix(m, "antigravity/") {
				t.Errorf("unexpected model in result: %s", m)
			}
		}
	})
}

func TestFilterModelsForProvider(t *testing.T) {
	t.Run("filters models by provider prefix", func(t *testing.T) {
		allModels := []string{
			"antigravity/claude-sonnet-4-5-thinking",
			"antigravity/claude-opus-4-5-thinking",
			"zai/claude-sonnet-4-5",
			"zai/gemini-3-flash",
		}

		zaiModels := filterModelsForProvider(allModels, "zai")
		if len(zaiModels) != 2 {
			t.Errorf("expected 2 zai models, got %d", len(zaiModels))
		}
		for _, m := range zaiModels {
			if !hasProviderPrefix(m, "zai") {
				t.Errorf("model %s should have zai prefix", m)
			}
		}

		agModels := filterModelsForProvider(allModels, "antigravity")
		if len(agModels) != 2 {
			t.Errorf("expected 2 antigravity models, got %d", len(agModels))
		}
		for _, m := range agModels {
			if !hasProviderPrefix(m, "antigravity") {
				t.Errorf("model %s should have antigravity prefix", m)
			}
		}
	})

	t.Run("returns empty slice for unknown provider", func(t *testing.T) {
		allModels := []string{
			"antigravity/claude-sonnet-4-5-thinking",
			"zai/claude-sonnet-4-5",
		}

		unknownModels := filterModelsForProvider(allModels, "unknown")
		if len(unknownModels) != 0 {
			t.Errorf("expected 0 models for unknown provider, got %d", len(unknownModels))
		}
	})
}

func TestHasProviderPrefix(t *testing.T) {
	tests := []struct {
		name     string
		modelID  string
		provider string
		expected bool
	}{
		{"zai model matches zai", "zai/claude-sonnet-4-5", "zai", true},
		{"zai model does not match antigravity", "zai/claude-sonnet-4-5", "antigravity", false},
		{"antigravity model matches antigravity", "antigravity/claude-opus-4-5-thinking", "antigravity", true},
		{"antigravity model does not match zai", "antigravity/claude-opus-4-5-thinking", "zai", false},
		{"model without prefix does not match", "claude-sonnet", "zai", false},
		{"empty model ID", "", "zai", false},
		{"empty provider", "zai/claude-sonnet-4-5", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasProviderPrefix(tt.modelID, tt.provider)
			if result != tt.expected {
				t.Errorf("hasProviderPrefix(%q, %q) = %v, want %v", tt.modelID, tt.provider, result, tt.expected)
			}
		})
	}
}
