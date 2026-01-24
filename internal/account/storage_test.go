package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigUnmarshal_AllowsNullInvalidReason(t *testing.T) {
	input := `{
		"accounts": [{
			"email": "a@example.com",
			"source": "oauth",
			"refreshToken": "rt",
			"isInvalid": false,
			"invalidReason": null,
			"modelRateLimits": {},
			"lastUsed": null
		}],
		"settings": {},
		"activeIndex": 0
	}`

	var cfg ConfigFile
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := string(cfg.Accounts[0].InvalidReason); got != "" {
		t.Fatalf("InvalidReason: expected empty, got %q", got)
	}
}

func TestStorageLoad_InvalidJSONReturnsEmptyConfig(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "accounts.json")

	if err := os.WriteFile(path, []byte("{not-json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := NewStorage(path)
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg == nil {
		t.Fatalf("Load returned nil config")
	}
	if len(cfg.Accounts) != 0 {
		t.Fatalf("expected empty accounts, got %d", len(cfg.Accounts))
	}
}
