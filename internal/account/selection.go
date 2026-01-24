package account

import (
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
)

// SelectionResult contains the result of an account selection operation.
type SelectionResult struct {
	Account  *Account
	NewIndex int
}

// isAccountUsable checks if an account is usable for a specific model.
func isAccountUsable(account *Account, modelID string) bool {
	if account == nil || account.IsInvalid {
		return false
	}

	now := time.Now().UnixMilli()
	if modelID != "" {
		if limit, ok := account.ModelRateLimits[modelID]; ok {
			if limit.IsRateLimited && limit.ResetTime > now {
				return false
			}
		}
	}

	return true
}

// isAccountPreferred checks if an account is preferred (not soft-limited) for a specific model.
func isAccountPreferred(account *Account, modelID string, settings Settings) bool {
	if !isAccountUsable(account, modelID) {
		return false
	}

	// If soft limits are disabled, all usable accounts are preferred
	if !settings.SoftLimitEnabled {
		return true
	}

	// Check if soft-limited for this model
	if modelID != "" {
		if limit, ok := account.ModelRateLimits[modelID]; ok {
			if limit.IsSoftLimited {
				return false
			}
		}
	}

	return true
}

// PickNext picks the next available account using round-robin selection.
func PickNext(accounts []Account, currentIndex int, modelID string, onSave func()) SelectionResult {
	return PickNextWithSettings(accounts, currentIndex, modelID, Settings{}, onSave)
}

// PickNextWithSettings picks the next available account, preferring non-soft-limited accounts.
// Uses simple round-robin selection without sticky behavior.
func PickNextWithSettings(accounts []Account, currentIndex int, modelID string, settings Settings, onSave func()) SelectionResult {
	ClearExpiredLimits(accounts)

	available := GetAvailableAccounts(accounts, modelID)
	if len(available) == 0 {
		return SelectionResult{Account: nil, NewIndex: currentIndex}
	}

	// Clamp index to valid range
	index := currentIndex
	if index >= len(accounts) {
		index = 0
	}

	// First pass: try to find a preferred (non-soft-limited) account
	if settings.SoftLimitEnabled {
		for i := 1; i <= len(accounts); i++ {
			idx := (index + i) % len(accounts)
			acc := &accounts[idx]

			if isAccountPreferred(acc, modelID, settings) {
				now := time.Now()
				acc.LastUsed = &now

				position := idx + 1
				total := len(accounts)
				utils.Info("[AccountManager] Using preferred account: %s (%d/%d)", acc.Email, position, total)

				if onSave != nil {
					go onSave()
				}

				return SelectionResult{Account: acc, NewIndex: idx}
			}
		}
	}

	// Second pass: fall back to any usable account (including soft-limited)
	for i := 1; i <= len(accounts); i++ {
		idx := (index + i) % len(accounts)
		acc := &accounts[idx]

		if isAccountUsable(acc, modelID) {
			now := time.Now()
			acc.LastUsed = &now

			position := idx + 1
			total := len(accounts)
			if settings.SoftLimitEnabled {
				utils.Warn("[AccountManager] Using soft-limited account: %s (%d/%d) - no preferred accounts available", acc.Email, position, total)
			} else {
				utils.Info("[AccountManager] Using account: %s (%d/%d)", acc.Email, position, total)
			}

			if onSave != nil {
				go onSave()
			}

			return SelectionResult{Account: acc, NewIndex: idx}
		}
	}

	return SelectionResult{Account: nil, NewIndex: currentIndex}
}
