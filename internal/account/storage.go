// Package account handles multi-account management with load balancing and failover.
package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
)

// Account represents a single account for a provider (Antigravity, Z.AI, or Copilot).
type Account struct {
	Email           string                    `json:"email"`
	Source          string                    `json:"source"`             // "oauth" or "manual"
	Provider        string                    `json:"provider,omitempty"` // "antigravity" (default), "zai", or "copilot"
	RefreshToken    string                    `json:"refreshToken,omitempty"`
	APIKey          string                    `json:"apiKey,omitempty"`
	ProjectID       string                    `json:"projectId,omitempty"`
	AccountType     string                    `json:"accountType,omitempty"` // For Copilot: "individual", "business", "enterprise"
	AddedAt         *time.Time                `json:"addedAt,omitempty"`
	IsInvalid       bool                      `json:"isInvalid,omitempty"`
	InvalidReason   NullableString            `json:"invalidReason,omitempty"`
	InvalidAt       *time.Time                `json:"invalidAt,omitempty"`
	ModelRateLimits map[string]ModelRateLimit `json:"modelRateLimits,omitempty"`
	LastUsed        *time.Time                `json:"lastUsed,omitempty"`
}

// ModelRateLimit tracks rate limit state for a specific model.
type ModelRateLimit struct {
	IsRateLimited  bool    `json:"isRateLimited"`
	ResetTime      int64   `json:"resetTime,omitempty"` // Unix timestamp in milliseconds
	IsSoftLimited  bool    `json:"isSoftLimited,omitempty"`
	QuotaRemaining float64 `json:"quotaRemaining,omitempty"` // 0.0 - 1.0 fraction
}

// Settings contains account manager settings.
type Settings struct {
	CooldownDurationMs int64   `json:"cooldownDurationMs,omitempty"`
	SoftLimitEnabled   bool    `json:"softLimitEnabled,omitempty"`
	SoftLimitThreshold float64 `json:"softLimitThreshold,omitempty"` // 0.0 - 1.0 fraction (default 0.20 = 20%)
}

// ConfigFile represents the account configuration file structure.
type ConfigFile struct {
	Accounts    []Account `json:"accounts"`
	Settings    Settings  `json:"settings"`
	ActiveIndex int       `json:"activeIndex"`
}

// Storage handles loading and saving account configuration.
type Storage struct {
	configPath string
	mu         sync.RWMutex
}

// NewStorage creates a new Storage instance.
func NewStorage(configPath string) *Storage {
	if configPath == "" {
		configPath = config.GetAccountConfigPath()
	}
	return &Storage{configPath: configPath}
}

// Load loads accounts from the configuration file.
// Returns empty config if file doesn't exist.
func (s *Storage) Load() (*ConfigFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file yet - return empty config
			utils.Info("[AccountManager] No config file found. Add an account using 'accounts add' command")
			return &ConfigFile{
				Accounts:    []Account{},
				Settings:    Settings{},
				ActiveIndex: 0,
			}, nil
		}
		// Node parity: treat unreadable config as "no config" (don't fail init).
		utils.Error("[AccountManager] Failed to load config: %v", err)
		return &ConfigFile{
			Accounts:    []Account{},
			Settings:    Settings{},
			ActiveIndex: 0,
		}, nil
	}

	var cfg ConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		// Node parity: treat parse errors as "no accounts" (don't fail init).
		utils.Error("[AccountManager] Failed to parse config: %v", err)
		return &ConfigFile{
			Accounts:    []Account{},
			Settings:    Settings{},
			ActiveIndex: 0,
		}, nil
	}

	// Initialize maps and reset invalid flag on startup
	for i := range cfg.Accounts {
		if cfg.Accounts[i].ModelRateLimits == nil {
			cfg.Accounts[i].ModelRateLimits = make(map[string]ModelRateLimit)
		}
		// Default provider to "antigravity" for backwards compatibility
		if cfg.Accounts[i].Provider == "" {
			cfg.Accounts[i].Provider = "antigravity"
		}
		// Reset invalid flag on startup - give accounts a fresh chance to refresh
		cfg.Accounts[i].IsInvalid = false
		cfg.Accounts[i].InvalidReason = ""
	}

	// Clamp activeIndex to valid range
	if cfg.ActiveIndex >= len(cfg.Accounts) {
		cfg.ActiveIndex = 0
	}

	utils.Info("[AccountManager] Loaded %d account(s) from config", len(cfg.Accounts))

	return &cfg, nil
}

// Save saves accounts to the configuration file atomically.
func (s *Storage) Save(cfg *ConfigFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure directory exists
	dir := filepath.Dir(s.configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Create serializable copy (exclude sensitive data from non-oauth sources)
	accounts := make([]Account, len(cfg.Accounts))
	for i, acc := range cfg.Accounts {
		accounts[i] = Account{
			Email:           acc.Email,
			Source:          acc.Source,
			Provider:        acc.Provider,
			ProjectID:       acc.ProjectID,
			AccountType:     acc.AccountType,
			AddedAt:         acc.AddedAt,
			IsInvalid:       acc.IsInvalid,
			InvalidReason:   acc.InvalidReason,
			ModelRateLimits: acc.ModelRateLimits,
			LastUsed:        acc.LastUsed,
		}
		// Only save refresh token for OAuth accounts
		if acc.Source == "oauth" {
			accounts[i].RefreshToken = acc.RefreshToken
		}
		// Only save API key for manual accounts
		if acc.Source == "manual" {
			accounts[i].APIKey = acc.APIKey
		}
	}

	output := ConfigFile{
		Accounts:    accounts,
		Settings:    cfg.Settings,
		ActiveIndex: cfg.ActiveIndex,
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename
	tempFile, err := os.CreateTemp(dir, ".accounts-*.tmp")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()

	// Ensure cleanup on error
	success := false
	defer func() {
		if !success {
			os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		return err
	}

	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return err
	}

	if err := tempFile.Close(); err != nil {
		return err
	}

	// Set permissions before rename
	if err := os.Chmod(tempPath, 0600); err != nil {
		return err
	}

	// Atomic rename
	if err := os.Rename(tempPath, s.configPath); err != nil {
		return err
	}

	success = true
	return nil
}

// ConfigPath returns the path to the configuration file.
func (s *Storage) ConfigPath() string {
	return s.configPath
}
