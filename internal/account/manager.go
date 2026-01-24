package account

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/auth"
	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

// TokenCacheEntry represents a cached access token.
type TokenCacheEntry struct {
	Token       string
	ExtractedAt time.Time
}

// Manager manages multiple Antigravity accounts with load balancing and failover.
type Manager struct {
	mu           sync.RWMutex
	accounts     []Account
	currentIndex int
	// currentIndexByProvider tracks round-robin selection independently per provider.
	// Values are indices into m.accounts (not provider-local indices).
	currentIndexByProvider map[string]int
	settings               Settings
	storage                *Storage
	initialized            bool

	// Per-account caches
	tokenCache   map[string]TokenCacheEntry // email -> token entry
	projectCache map[string]string          // email -> projectId
}

// NewManager creates a new AccountManager.
func NewManager(configPath string) *Manager {
	return &Manager{
		storage:                NewStorage(configPath),
		tokenCache:             make(map[string]TokenCacheEntry),
		projectCache:           make(map[string]string),
		currentIndexByProvider: make(map[string]int),
	}
}

// Initialize loads the account configuration.
func (m *Manager) Initialize() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initialized {
		return nil
	}

	cfg, err := m.storage.Load()
	if err != nil {
		return fmt.Errorf("failed to load accounts: %w", err)
	}

	m.accounts = cfg.Accounts
	m.settings = cfg.Settings
	m.currentIndex = cfg.ActiveIndex
	// Backwards-compat: ActiveIndex historically tracked Antigravity selection.
	m.currentIndexByProvider["antigravity"] = m.currentIndex

	// Clear any expired rate limits
	m.clearExpiredLimitsLocked()

	m.initialized = true
	return nil
}

// GetAccountCount returns the number of accounts.
func (m *Manager) GetAccountCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.accounts)
}

// GetAccountCountByProvider returns the number of accounts for a specific provider.
func (m *Manager) GetAccountCountByProvider(provider string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, acc := range m.accounts {
		if acc.Provider == provider {
			count++
		}
	}
	return count
}

// IsAllRateLimited checks if all accounts are rate-limited for a model.
func (m *Manager) IsAllRateLimited(modelID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return IsAllRateLimited(m.accounts, modelID)
}

// IsAllRateLimitedByProvider checks if all accounts for a provider are rate-limited for a model.
func (m *Manager) IsAllRateLimitedByProvider(provider, modelID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if modelID == "" {
		return false
	}
	count := 0
	now := time.Now().UnixMilli()
	for _, acc := range m.accounts {
		if acc.Provider != provider {
			continue
		}
		count++
		if acc.IsInvalid {
			continue
		}
		limit, ok := acc.ModelRateLimits[modelID]
		if !ok || !limit.IsRateLimited || limit.ResetTime <= now {
			return false
		}
	}
	// No accounts for this provider: not rate-limited (handled as "no accounts" upstream).
	if count == 0 {
		return false
	}
	return true
}

// GetAvailableAccounts returns non-rate-limited accounts.
func (m *Manager) GetAvailableAccounts(modelID string) []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return GetAvailableAccounts(m.accounts, modelID)
}

// GetInvalidAccounts returns invalid accounts.
func (m *Manager) GetInvalidAccounts() []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return GetInvalidAccounts(m.accounts)
}

// ClearExpiredLimits clears expired rate limits.
func (m *Manager) ClearExpiredLimits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clearExpiredLimitsLocked()
}

func (m *Manager) clearExpiredLimitsLocked() int {
	cleared := ClearExpiredLimits(m.accounts)
	if cleared > 0 {
		go m.saveToDiskAsync()
	}
	return cleared
}

// ResetAllRateLimits clears all rate limits (optimistic retry).
func (m *Manager) ResetAllRateLimits() {
	m.mu.Lock()
	defer m.mu.Unlock()
	ResetAllRateLimits(m.accounts)
}

// PickNext picks the next available account.
func (m *Manager) PickNext(modelID string) *Account {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := PickNextWithSettings(m.accounts, m.currentIndex, modelID, m.settings, func() { go m.saveToDiskAsync() })
	m.currentIndex = result.NewIndex
	return result.Account
}

// MarkRateLimited marks an account as rate-limited.
func (m *Manager) MarkRateLimited(email string, resetMs int64, modelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	MarkRateLimited(m.accounts, email, resetMs, m.settings, modelID)
	go m.saveToDiskAsync()
}

// MarkInvalid marks an account as invalid.
func (m *Manager) MarkInvalid(email string, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	MarkInvalid(m.accounts, email, reason)
	go m.saveToDiskAsync()
}

// GetMinWaitTimeMs returns the minimum wait time until any account is available.
func (m *Manager) GetMinWaitTimeMs(modelID string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return GetMinWaitTimeMs(m.accounts, modelID)
}

// GetMinWaitTimeMsByProvider returns the minimum wait time for a specific provider.
func (m *Manager) GetMinWaitTimeMsByProvider(provider, modelID string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if modelID == "" {
		return 0
	}

	if !m.isAllRateLimitedByProviderLocked(provider, modelID) {
		return 0
	}

	now := time.Now().UnixMilli()
	var minWait int64 = -1
	for i := range m.accounts {
		acc := &m.accounts[i]
		if acc.Provider != provider {
			continue
		}
		if limit, ok := acc.ModelRateLimits[modelID]; ok {
			if limit.IsRateLimited && limit.ResetTime > 0 {
				wait := limit.ResetTime - now
				if wait > 0 && (minWait < 0 || wait < minWait) {
					minWait = wait
				}
			}
		}
	}

	if minWait < 0 {
		return int64(config.DefaultCooldownDuration / time.Millisecond)
	}
	return minWait
}

// PickNextByProvider picks the next available account for a specific provider.
func (m *Manager) PickNextByProvider(provider, modelID string) *Account {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clearExpiredLimitsLocked()

	if m.getAccountCountByProviderLocked(provider) == 0 {
		return nil
	}

	return m.pickNextByProviderLocked(provider, modelID)
}

func (m *Manager) getAccountCountByProviderLocked(provider string) int {
	count := 0
	for _, acc := range m.accounts {
		if acc.Provider == provider {
			count++
		}
	}
	return count
}

func (m *Manager) ensureProviderIndexLocked(provider string) int {
	if len(m.accounts) == 0 {
		return -1
	}

	idx, ok := m.currentIndexByProvider[provider]
	if ok && idx >= 0 && idx < len(m.accounts) && m.accounts[idx].Provider == provider {
		return idx
	}

	// Fall back to the first account for this provider.
	for i := range m.accounts {
		if m.accounts[i].Provider == provider {
			m.currentIndexByProvider[provider] = i
			if provider == "antigravity" {
				m.currentIndex = i
			}
			return i
		}
	}

	return -1
}

func (m *Manager) isAccountUsableForModelLocked(acc *Account, modelID string) bool {
	if acc == nil || acc.IsInvalid {
		return false
	}
	if modelID == "" {
		return true
	}

	now := time.Now().UnixMilli()
	if limit, ok := acc.ModelRateLimits[modelID]; ok {
		if limit.IsRateLimited && limit.ResetTime > now {
			return false
		}
	}

	return true
}

func (m *Manager) isAccountPreferredForModelLocked(acc *Account, modelID string) bool {
	if !m.isAccountUsableForModelLocked(acc, modelID) {
		return false
	}
	if !m.settings.SoftLimitEnabled {
		return true
	}
	if modelID == "" {
		return true
	}
	if limit, ok := acc.ModelRateLimits[modelID]; ok {
		if limit.IsSoftLimited {
			return false
		}
	}
	return true
}

func (m *Manager) pickNextByProviderLocked(provider, modelID string) *Account {
	start := m.ensureProviderIndexLocked(provider)
	if start < 0 {
		return nil
	}

	// First pass: try preferred (non-soft-limited) accounts.
	if m.settings.SoftLimitEnabled {
		for i := 1; i <= len(m.accounts); i++ {
			idx := (start + i) % len(m.accounts)
			acc := &m.accounts[idx]
			if acc.Provider != provider {
				continue
			}
			if m.isAccountPreferredForModelLocked(acc, modelID) {
				now := time.Now()
				acc.LastUsed = &now
				m.currentIndexByProvider[provider] = idx
				if provider == "antigravity" {
					m.currentIndex = idx
				}
				go m.saveToDiskAsync()
				utils.Info("[AccountManager] Using preferred account: %s", acc.Email)
				return acc
			}
		}
	}

	// Second pass: any usable account (including soft-limited).
	for i := 1; i <= len(m.accounts); i++ {
		idx := (start + i) % len(m.accounts)
		acc := &m.accounts[idx]
		if acc.Provider != provider {
			continue
		}
		if m.isAccountUsableForModelLocked(acc, modelID) {
			now := time.Now()
			acc.LastUsed = &now
			m.currentIndexByProvider[provider] = idx
			if provider == "antigravity" {
				m.currentIndex = idx
			}
			go m.saveToDiskAsync()
			if m.settings.SoftLimitEnabled {
				utils.Warn("[AccountManager] Using soft-limited account: %s - no preferred accounts available", acc.Email)
			} else {
				utils.Info("[AccountManager] Using account: %s", acc.Email)
			}
			return acc
		}
	}

	return nil
}

func (m *Manager) isAllRateLimitedByProviderLocked(provider, modelID string) bool {
	if modelID == "" {
		return false
	}
	count := 0
	now := time.Now().UnixMilli()
	for _, acc := range m.accounts {
		if acc.Provider != provider {
			continue
		}
		count++
		if acc.IsInvalid {
			continue
		}
		limit, ok := acc.ModelRateLimits[modelID]
		if !ok || !limit.IsRateLimited || limit.ResetTime <= now {
			return false
		}
	}
	return count > 0
}

// ResetAllRateLimitsByProvider clears all rate limits for a specific provider (optimistic retry).
func (m *Manager) ResetAllRateLimitsByProvider(provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.accounts {
		if m.accounts[i].Provider == provider {
			for modelID, limit := range m.accounts[i].ModelRateLimits {
				m.accounts[i].ModelRateLimits[modelID] = ModelRateLimit{
					IsRateLimited:  false,
					ResetTime:      0,
					IsSoftLimited:  limit.IsSoftLimited,
					QuotaRemaining: limit.QuotaRemaining,
				}
			}
		}
	}
	utils.Warn("[AccountManager] Reset all rate limits for provider %s (optimistic retry)", provider)
}

// GetTokenForAccount gets an OAuth token for an account.
func (m *Manager) GetTokenForAccount(account *Account) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check cache first
	if cached, ok := m.tokenCache[account.Email]; ok {
		if time.Since(cached.ExtractedAt) < config.TokenRefreshInterval {
			return cached.Token, nil
		}
	}

	var token string

	switch account.Source {
	case "oauth":
		if account.RefreshToken == "" {
			return "", fmt.Errorf("no refresh token for OAuth account")
		}
		tokens, err := auth.RefreshAccessToken(account.RefreshToken)
		if err != nil {
			// Check if it's a network error (shouldn't mark invalid)
			if isNetworkError(err) {
				return "", fmt.Errorf("AUTH_NETWORK_ERROR: %v", err)
			}
			// Mark as invalid
			MarkInvalid(m.accounts, account.Email, err.Error())
			go m.saveToDiskAsync()
			return "", fmt.Errorf("AUTH_INVALID: %s: %v", account.Email, err)
		}
		token = tokens.AccessToken
		// Clear invalid flag on success
		if account.IsInvalid {
			account.IsInvalid = false
			account.InvalidReason = ""
			go m.saveToDiskAsync()
		}
		utils.Success("[AccountManager] Refreshed OAuth token for: %s", account.Email)

	case "manual":
		if account.APIKey == "" {
			return "", fmt.Errorf("no API key for manual account")
		}
		token = account.APIKey

	default:
		return "", fmt.Errorf("unknown account source: %s", account.Source)
	}

	// Cache the token
	m.tokenCache[account.Email] = TokenCacheEntry{
		Token:       token,
		ExtractedAt: time.Now(),
	}

	return token, nil
}

// GetProjectForAccount gets the project ID for an account.
func (m *Manager) GetProjectForAccount(account *Account, token string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check cache first
	if cached, ok := m.projectCache[account.Email]; ok {
		return cached, nil
	}

	// Use account's projectId if specified
	if account.ProjectID != "" {
		m.projectCache[account.Email] = account.ProjectID
		return account.ProjectID, nil
	}

	// Discover project via loadCodeAssist API
	projectID, err := auth.DiscoverProjectID(token)
	if err != nil {
		utils.Warn("[AccountManager] Project discovery failed, using default: %v", err)
		projectID = config.DefaultProjectID
	}

	m.projectCache[account.Email] = projectID
	return projectID, nil
}

// ClearProjectCache clears the project cache.
func (m *Manager) ClearProjectCache(email string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if email == "" {
		m.projectCache = make(map[string]string)
	} else {
		delete(m.projectCache, email)
	}
}

// ClearTokenCache clears the token cache.
func (m *Manager) ClearTokenCache(email string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if email == "" {
		m.tokenCache = make(map[string]TokenCacheEntry)
	} else {
		delete(m.tokenCache, email)
	}
}

// SaveToDisk saves the current state to disk.
func (m *Manager) SaveToDisk() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.saveToDiskLocked()
}

// saveToDiskLocked saves without acquiring the lock (caller must hold lock).
func (m *Manager) saveToDiskLocked() error {
	cfg := &ConfigFile{
		Accounts:    m.accounts,
		Settings:    m.settings,
		ActiveIndex: m.currentIndex,
	}

	return m.storage.Save(cfg)
}

func (m *Manager) saveToDiskAsync() {
	if err := m.SaveToDisk(); err != nil {
		utils.Error("[AccountManager] Failed to save config: %v", err)
	}
}

// GetStatus returns status information for logging/API.
func (m *Manager) GetStatus() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	available := GetAvailableAccounts(m.accounts, "")
	invalid := GetInvalidAccounts(m.accounts)

	// Count accounts that have any active model-specific rate limits
	rateLimited := 0
	now := time.Now().UnixMilli()
	for _, acc := range m.accounts {
		for _, limit := range acc.ModelRateLimits {
			if limit.IsRateLimited && limit.ResetTime > now {
				rateLimited++
				break
			}
		}
	}

	accountsInfo := make([]map[string]interface{}, len(m.accounts))
	for i, acc := range m.accounts {
		accountsInfo[i] = map[string]interface{}{
			"email":           acc.Email,
			"source":          acc.Source,
			"modelRateLimits": acc.ModelRateLimits,
			"isInvalid":       acc.IsInvalid,
			"invalidReason":   acc.InvalidReason,
			"lastUsed":        acc.LastUsed,
		}
	}

	return map[string]interface{}{
		"total":       len(m.accounts),
		"available":   len(available),
		"rateLimited": rateLimited,
		"invalid":     len(invalid),
		"summary": fmt.Sprintf("%d total, %d available, %d rate-limited, %d invalid",
			len(m.accounts), len(available), rateLimited, len(invalid)),
		"accounts": accountsInfo,
	}
}

// GetAccountStatuses returns typed account status information for the API.
func (m *Manager) GetAccountStatuses() []types.AccountStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now().UnixMilli()
	statuses := make([]types.AccountStatus, len(m.accounts))

	// Get all supported models
	supportedModels := []string{
		"claude-sonnet-4-5-thinking",
		"claude-opus-4-5-thinking",
		"claude-sonnet-4-5",
		"gemini-3-flash",
		"gemini-3-pro-low",
		"gemini-3-pro-high",
	}

	for i, acc := range m.accounts {
		status := types.AccountStatus{
			Email:    acc.Email,
			Status:   "ok",
			LastUsed: acc.LastUsed,
			Limits:   make(map[string]types.ModelQuota),
		}

		if acc.IsInvalid {
			status.Status = "invalid"
			status.Error = string(acc.InvalidReason)
		}

		// Initialize all models with 100% remaining
		for _, modelID := range supportedModels {
			status.Limits[modelID] = types.ModelQuota{
				RemainingFraction:   1.0,
				RemainingPercentage: 100,
				ResetTime:           nil,
			}
		}

		// Update with actual rate limits
		for modelID, limit := range acc.ModelRateLimits {
			if limit.IsRateLimited && limit.ResetTime > now {
				resetTime := time.UnixMilli(limit.ResetTime)
				status.Limits[modelID] = types.ModelQuota{
					RemainingFraction:   0.0,
					RemainingPercentage: 0,
					ResetTime:           &resetTime,
				}
				status.Status = "rate-limited"
			}
		}

		statuses[i] = status
	}

	return statuses
}

// GetSettings returns the current settings.
func (m *Manager) GetSettings() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

// SetSoftLimitSettings configures soft limit behavior.
func (m *Manager) SetSoftLimitSettings(enabled bool, threshold float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.SoftLimitEnabled = enabled
	m.settings.SoftLimitThreshold = threshold
}

// UpdateSoftLimitStatus updates the soft limit status for an account/model based on quota.
// This should be called after each request with the current remaining quota fraction.
// Use persist=true for normal operation, persist=false for read-only status checks (like /health).
func (m *Manager) UpdateSoftLimitStatus(email string, modelID string, remainingFraction float64) {
	m.updateSoftLimitStatusInternal(email, modelID, remainingFraction, true)
}

// UpdateSoftLimitStatusNoPersist updates soft limit status without persisting to disk or logging.
// Use this for read-only status refresh paths like /health to avoid disk/log churn.
func (m *Manager) UpdateSoftLimitStatusNoPersist(email string, modelID string, remainingFraction float64) {
	m.updateSoftLimitStatusInternal(email, modelID, remainingFraction, false)
}

func (m *Manager) updateSoftLimitStatusInternal(email string, modelID string, remainingFraction float64, persist bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.settings.SoftLimitEnabled {
		return
	}

	// Validate remainingFraction: treat NaN/Inf as soft-limited (conservative)
	// and clamp to [0, 1] range
	if math.IsNaN(remainingFraction) || math.IsInf(remainingFraction, 0) {
		utils.Debug("[AccountManager] Invalid remainingFraction for %s/%s: %v, treating as soft-limited",
			email, modelID, remainingFraction)
		remainingFraction = 0 // Treat as exhausted
	} else if remainingFraction < 0 {
		utils.Debug("[AccountManager] Clamping negative remainingFraction for %s/%s: %v -> 0",
			email, modelID, remainingFraction)
		remainingFraction = 0
	} else if remainingFraction > 1 {
		utils.Debug("[AccountManager] Clamping remainingFraction > 1 for %s/%s: %v -> 1",
			email, modelID, remainingFraction)
		remainingFraction = 1
	}

	for i := range m.accounts {
		if m.accounts[i].Email != email {
			continue
		}

		if m.accounts[i].ModelRateLimits == nil {
			m.accounts[i].ModelRateLimits = make(map[string]ModelRateLimit)
		}

		limit := m.accounts[i].ModelRateLimits[modelID]
		oldSoftLimited := limit.IsSoftLimited

		limit.QuotaRemaining = remainingFraction
		// Treat 0% (exhausted) as soft-limited too - explicitly check <= 0
		limit.IsSoftLimited = remainingFraction <= 0 || remainingFraction < m.settings.SoftLimitThreshold

		m.accounts[i].ModelRateLimits[modelID] = limit

		// Log and persist on ANY state transition (into OR out of soft-limited)
		if persist && limit.IsSoftLimited != oldSoftLimited {
			if limit.IsSoftLimited {
				utils.Warn("[AccountManager] Account %s is soft-limited for %s (%.0f%% remaining, threshold %.0f%%)",
					email, modelID, remainingFraction*100, m.settings.SoftLimitThreshold*100)
			} else {
				utils.Info("[AccountManager] Account %s is no longer soft-limited for %s (%.0f%% remaining)",
					email, modelID, remainingFraction*100)
			}
			go m.saveToDiskAsync()
		}
		return
	}
}

// IsSoftLimited checks if an account is soft-limited for a specific model.
func (m *Manager) IsSoftLimited(email string, modelID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.settings.SoftLimitEnabled {
		return false
	}

	for _, acc := range m.accounts {
		if acc.Email == email {
			if limit, ok := acc.ModelRateLimits[modelID]; ok {
				return limit.IsSoftLimited
			}
			return false
		}
	}
	return false
}

// IsSoftLimitEnabled returns whether soft limits are enabled.
func (m *Manager) IsSoftLimitEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.SoftLimitEnabled
}

// GetSoftLimitThreshold returns the current soft limit threshold.
func (m *Manager) GetSoftLimitThreshold() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.SoftLimitThreshold
}

// GetPreferredAccounts returns accounts that are not soft-limited for the given model.
// This is used by selection logic to prefer non-soft-limited accounts.
func (m *Manager) GetPreferredAccounts(modelID string) []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return GetPreferredAccounts(m.accounts, modelID, m.settings)
}

// GetAllAccounts returns all accounts (for quota fetching).
func (m *Manager) GetAllAccounts() []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Account, len(m.accounts))
	for i := range m.accounts {
		acc := m.accounts[i] // copy

		// Deep-copy maps/pointers so callers can safely inspect without holding the lock.
		if acc.ModelRateLimits != nil {
			limitsCopy := make(map[string]ModelRateLimit, len(acc.ModelRateLimits))
			for k, v := range acc.ModelRateLimits {
				limitsCopy[k] = v
			}
			acc.ModelRateLimits = limitsCopy
		} else {
			acc.ModelRateLimits = make(map[string]ModelRateLimit)
		}

		if acc.AddedAt != nil {
			t := *acc.AddedAt
			acc.AddedAt = &t
		}
		if acc.InvalidAt != nil {
			t := *acc.InvalidAt
			acc.InvalidAt = &t
		}
		if acc.LastUsed != nil {
			t := *acc.LastUsed
			acc.LastUsed = &t
		}

		result[i] = acc
	}
	return result
}

// GetAllAccountsByProvider returns all accounts for a specific provider (deep-copied).
func (m *Manager) GetAllAccountsByProvider(provider string) []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Account, 0)
	for i := range m.accounts {
		if m.accounts[i].Provider != provider {
			continue
		}
		acc := m.accounts[i] // copy

		// Deep-copy maps/pointers so callers can safely inspect without holding the lock.
		if acc.ModelRateLimits != nil {
			limitsCopy := make(map[string]ModelRateLimit, len(acc.ModelRateLimits))
			for k, v := range acc.ModelRateLimits {
				limitsCopy[k] = v
			}
			acc.ModelRateLimits = limitsCopy
		} else {
			acc.ModelRateLimits = make(map[string]ModelRateLimit)
		}

		if acc.AddedAt != nil {
			t := *acc.AddedAt
			acc.AddedAt = &t
		}
		if acc.InvalidAt != nil {
			t := *acc.InvalidAt
			acc.InvalidAt = &t
		}
		if acc.LastUsed != nil {
			t := *acc.LastUsed
			acc.LastUsed = &t
		}

		result = append(result, acc)
	}
	return result
}

// AddAccount adds a new account to the pool.
func (m *Manager) AddAccount(account Account) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for duplicate
	for _, acc := range m.accounts {
		if acc.Email == account.Email {
			return fmt.Errorf("account %s already exists", account.Email)
		}
	}

	// Check max accounts
	if len(m.accounts) >= config.MaxAccounts {
		return fmt.Errorf("maximum number of accounts (%d) reached", config.MaxAccounts)
	}

	// Initialize maps
	if account.ModelRateLimits == nil {
		account.ModelRateLimits = make(map[string]ModelRateLimit)
	}

	now := time.Now()
	account.AddedAt = &now

	m.accounts = append(m.accounts, account)

	// Save synchronously for CLI commands (async would exit before write completes)
	if err := m.saveToDiskLocked(); err != nil {
		// Remove the account we just added since save failed
		m.accounts = m.accounts[:len(m.accounts)-1]
		return fmt.Errorf("failed to save account: %w", err)
	}

	utils.Success("[AccountManager] Added account: %s", account.Email)
	return nil
}

// RemoveAccount removes an account from the pool.
func (m *Manager) RemoveAccount(email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, acc := range m.accounts {
		if acc.Email == email {
			removed := acc
			removedIdx := i
			m.accounts = append(m.accounts[:i], m.accounts[i+1:]...)

			// Clear caches
			delete(m.tokenCache, email)
			delete(m.projectCache, email)

			// Adjust current index if needed
			if m.currentIndex >= len(m.accounts) {
				m.currentIndex = 0
			}

			// Adjust per-provider indices: delete entries pointing to the removed index
			// and decrement indices greater than the removed index.
			for provider, idx := range m.currentIndexByProvider {
				if idx == removedIdx {
					// This provider was pointing to the removed account - reset to first for that provider.
					delete(m.currentIndexByProvider, provider)
				} else if idx > removedIdx {
					// Shift down indices that were after the removed account.
					m.currentIndexByProvider[provider] = idx - 1
				}
			}

			// Save synchronously for CLI commands
			if err := m.saveToDiskLocked(); err != nil {
				// Restore the account since save failed
				m.accounts = append(m.accounts[:i], append([]Account{removed}, m.accounts[i:]...)...)
				return fmt.Errorf("failed to save after removal: %w", err)
			}

			utils.Success("[AccountManager] Removed account: %s", email)
			return nil
		}
	}

	return fmt.Errorf("account %s not found", email)
}

// isNetworkError checks if an error is a transient network error.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsAny(msg, []string{
		"connection refused",
		"no such host",
		"network is unreachable",
		"timeout",
		"temporary failure",
		"ETIMEDOUT",
		"ECONNREFUSED",
		"ENOTFOUND",
	})
}

func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
