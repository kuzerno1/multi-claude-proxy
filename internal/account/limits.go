package account

import (
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
)

// IsAllRateLimited checks if all accounts are rate-limited for a specific model.
func IsAllRateLimited(accounts []Account, modelID string) bool {
	if len(accounts) == 0 {
		return true
	}
	if modelID == "" {
		return false // No model specified = not rate limited
	}

	now := time.Now().UnixMilli()
	for _, acc := range accounts {
		if acc.IsInvalid {
			continue // Invalid accounts count as unavailable
		}
		limit, ok := acc.ModelRateLimits[modelID]
		if !ok || !limit.IsRateLimited || limit.ResetTime <= now {
			return false // Found an available account
		}
	}
	return true
}

// GetAvailableAccounts returns accounts that are not rate-limited or invalid for a model.
func GetAvailableAccounts(accounts []Account, modelID string) []Account {
	available := make([]Account, 0)
	now := time.Now().UnixMilli()

	for _, acc := range accounts {
		if acc.IsInvalid {
			continue
		}

		if modelID != "" {
			if limit, ok := acc.ModelRateLimits[modelID]; ok {
				if limit.IsRateLimited && limit.ResetTime > now {
					continue
				}
			}
		}

		available = append(available, acc)
	}

	return available
}

// GetInvalidAccounts returns accounts that are marked as invalid.
func GetInvalidAccounts(accounts []Account) []Account {
	invalid := make([]Account, 0)
	for _, acc := range accounts {
		if acc.IsInvalid {
			invalid = append(invalid, acc)
		}
	}
	return invalid
}

// ClearExpiredLimits clears rate limits that have expired.
// Returns the number of limits cleared.
// Preserves soft limit status and quota remaining values.
func ClearExpiredLimits(accounts []Account) int {
	now := time.Now().UnixMilli()
	cleared := 0

	for i := range accounts {
		for modelID, limit := range accounts[i].ModelRateLimits {
			if limit.IsRateLimited && limit.ResetTime <= now {
				accounts[i].ModelRateLimits[modelID] = ModelRateLimit{
					IsRateLimited:  false,
					ResetTime:      0,
					IsSoftLimited:  limit.IsSoftLimited,  // Preserve soft limit status
					QuotaRemaining: limit.QuotaRemaining, // Preserve quota info
				}
				cleared++
				utils.Success("[AccountManager] Rate limit expired for: %s (model: %s)", accounts[i].Email, modelID)
			}
		}
	}

	return cleared
}

// ResetAllRateLimits clears all rate limits (optimistic retry strategy).
// Preserves soft limit status and quota remaining values.
func ResetAllRateLimits(accounts []Account) {
	for i := range accounts {
		for modelID, limit := range accounts[i].ModelRateLimits {
			accounts[i].ModelRateLimits[modelID] = ModelRateLimit{
				IsRateLimited:  false,
				ResetTime:      0,
				IsSoftLimited:  limit.IsSoftLimited,  // Preserve soft limit status
				QuotaRemaining: limit.QuotaRemaining, // Preserve quota info
			}
		}
	}
	utils.Warn("[AccountManager] Reset all rate limits for optimistic retry")
}

// MarkRateLimited marks an account as rate-limited for a specific model.
// Returns true if the account was found and marked.
// Preserves soft limit status and quota remaining values.
func MarkRateLimited(accounts []Account, email string, resetMs int64, settings Settings, modelID string) bool {
	for i := range accounts {
		if accounts[i].Email == email {
			cooldownMs := resetMs
			if cooldownMs == 0 {
				if settings.CooldownDurationMs > 0 {
					cooldownMs = settings.CooldownDurationMs
				} else {
					cooldownMs = int64(config.DefaultCooldownDuration / time.Millisecond)
				}
			}

			resetTime := time.Now().UnixMilli() + cooldownMs

			if accounts[i].ModelRateLimits == nil {
				accounts[i].ModelRateLimits = make(map[string]ModelRateLimit)
			}

			// Preserve existing soft limit info
			existingLimit := accounts[i].ModelRateLimits[modelID]
			accounts[i].ModelRateLimits[modelID] = ModelRateLimit{
				IsRateLimited:  true,
				ResetTime:      resetTime,
				IsSoftLimited:  existingLimit.IsSoftLimited,
				QuotaRemaining: existingLimit.QuotaRemaining,
			}

			utils.Warn("[AccountManager] Rate limited: %s (model: %s). Available in %s",
				email, modelID, utils.FormatDuration(time.Duration(cooldownMs)*time.Millisecond))

			return true
		}
	}
	return false
}

// MarkInvalid marks an account as invalid (credentials need re-authentication).
// Returns true if the account was found and marked.
func MarkInvalid(accounts []Account, email string, reason string) bool {
	for i := range accounts {
		if accounts[i].Email == email {
			now := time.Now()
			accounts[i].IsInvalid = true
			accounts[i].InvalidReason = NullableString(reason)
			accounts[i].InvalidAt = &now

			utils.Error("[AccountManager] Account INVALID: %s", email)
			utils.Error("[AccountManager]   Reason: %s", reason)
			utils.Error("[AccountManager]   Run 'multi-claude-proxy accounts' to re-authenticate this account")

			return true
		}
	}
	return false
}

// GetMinWaitTimeMs returns the minimum time until any account becomes available.
func GetMinWaitTimeMs(accounts []Account, modelID string) int64 {
	if !IsAllRateLimited(accounts, modelID) {
		return 0
	}

	now := time.Now().UnixMilli()
	var minWait int64 = -1
	var soonestAccount *Account

	for i := range accounts {
		if modelID != "" {
			if limit, ok := accounts[i].ModelRateLimits[modelID]; ok {
				if limit.IsRateLimited && limit.ResetTime > 0 {
					wait := limit.ResetTime - now
					if wait > 0 && (minWait < 0 || wait < minWait) {
						minWait = wait
						soonestAccount = &accounts[i]
					}
				}
			}
		}
	}

	if soonestAccount != nil {
		utils.Info("[AccountManager] Shortest wait: %s (account: %s)",
			utils.FormatDuration(time.Duration(minWait)*time.Millisecond), soonestAccount.Email)
	}

	if minWait < 0 {
		return int64(config.DefaultCooldownDuration / time.Millisecond)
	}
	return minWait
}

// GetPreferredAccounts returns accounts that are not soft-limited for the given model.
// These accounts should be preferred for selection to avoid draining accounts to 0%.
// If soft limits are disabled or no accounts are preferred, returns all available accounts.
func GetPreferredAccounts(accounts []Account, modelID string, settings Settings) []Account {
	if !settings.SoftLimitEnabled {
		return GetAvailableAccounts(accounts, modelID)
	}

	available := GetAvailableAccounts(accounts, modelID)
	preferred := make([]Account, 0, len(available))

	for _, acc := range available {
		if modelID != "" {
			if limit, ok := acc.ModelRateLimits[modelID]; ok {
				if limit.IsSoftLimited {
					continue // Skip soft-limited accounts
				}
			}
		}
		preferred = append(preferred, acc)
	}

	// If all available accounts are soft-limited, fall back to using all available
	if len(preferred) == 0 {
		return available
	}

	return preferred
}

// HasNonSoftLimitedAccounts returns true if there are available accounts that are NOT soft-limited.
// Unlike GetPreferredAccounts, this does NOT fall back to available accounts when all are soft-limited.
func HasNonSoftLimitedAccounts(accounts []Account, modelID string, settings Settings) bool {
	if !settings.SoftLimitEnabled {
		return len(GetAvailableAccounts(accounts, modelID)) > 0
	}

	available := GetAvailableAccounts(accounts, modelID)
	for _, acc := range available {
		isSoftLimited := false
		if modelID != "" {
			if limit, ok := acc.ModelRateLimits[modelID]; ok {
				isSoftLimited = limit.IsSoftLimited
			}
		}
		if !isSoftLimited {
			return true
		}
	}
	return false
}

// IsAccountSoftLimited checks if an account is soft-limited for a specific model.
func IsAccountSoftLimited(acc *Account, modelID string, settings Settings) bool {
	if !settings.SoftLimitEnabled {
		return false
	}

	if modelID != "" {
		if limit, ok := acc.ModelRateLimits[modelID]; ok {
			return limit.IsSoftLimited
		}
	}
	return false
}

// GetSoftLimitedCount returns the count of accounts that are soft-limited for a model.
func GetSoftLimitedCount(accounts []Account, modelID string, settings Settings) int {
	if !settings.SoftLimitEnabled {
		return 0
	}

	count := 0
	for _, acc := range accounts {
		if acc.IsInvalid {
			continue
		}
		if modelID != "" {
			if limit, ok := acc.ModelRateLimits[modelID]; ok {
				if limit.IsSoftLimited {
					count++
				}
			}
		}
	}
	return count
}
