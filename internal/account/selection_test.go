package account

import (
	"testing"
	"time"
)

// Helper to create a test account with optional soft limit
func testAccount(email string, softLimited bool, rateLimited bool, invalid bool) Account {
	acc := Account{
		Email:           email,
		Source:          "oauth",
		IsInvalid:       invalid,
		ModelRateLimits: make(map[string]ModelRateLimit),
	}

	if softLimited || rateLimited {
		acc.ModelRateLimits["claude-sonnet-4-5"] = ModelRateLimit{
			IsSoftLimited: softLimited,
			IsRateLimited: rateLimited,
			ResetTime:     time.Now().Add(1 * time.Hour).UnixMilli(), // Future reset time
		}
	}

	return acc
}

func TestPickNextWithSettings_PrefersNonSoftLimitedAccounts(t *testing.T) {
	accounts := []Account{
		testAccount("soft1@example.com", true, false, false),
		testAccount("normal@example.com", false, false, false),
		testAccount("soft2@example.com", true, false, false),
	}

	settings := Settings{SoftLimitEnabled: true, SoftLimitThreshold: 0.20}

	result := PickNextWithSettings(accounts, 0, "claude-sonnet-4-5", settings, nil)

	if result.Account == nil {
		t.Fatal("expected to find an account, got nil")
	}
	if result.Account.Email != "normal@example.com" {
		t.Errorf("expected normal@example.com, got %s", result.Account.Email)
	}
}

func TestPickNextWithSettings_FallsBackToSoftLimitedWhenNoAlternatives(t *testing.T) {
	accounts := []Account{
		testAccount("soft1@example.com", true, false, false),
		testAccount("soft2@example.com", true, false, false),
	}

	settings := Settings{SoftLimitEnabled: true, SoftLimitThreshold: 0.20}

	result := PickNextWithSettings(accounts, 0, "claude-sonnet-4-5", settings, nil)

	if result.Account == nil {
		t.Fatal("expected to find an account (fallback to soft-limited), got nil")
	}
	// Should get the next soft-limited account (index 1, since we start at 0)
	if result.Account.Email != "soft1@example.com" && result.Account.Email != "soft2@example.com" {
		t.Errorf("expected a soft-limited account, got %s", result.Account.Email)
	}
}

func TestPickNextWithSettings_SkipsSoftLimitsWhenDisabled(t *testing.T) {
	accounts := []Account{
		testAccount("normal@example.com", false, false, false),
		testAccount("soft@example.com", true, false, false),
	}

	// Soft limits disabled - should use round-robin without checking soft limit status
	settings := Settings{SoftLimitEnabled: false}

	result := PickNextWithSettings(accounts, 0, "claude-sonnet-4-5", settings, nil)

	if result.Account == nil {
		t.Fatal("expected to find an account, got nil")
	}
	// Should get soft@ (index 1) because round-robin from index 0 checks index 1 first
	if result.Account.Email != "soft@example.com" {
		t.Errorf("expected soft@example.com (round-robin, soft limits disabled), got %s", result.Account.Email)
	}
}

func TestPickNextWithSettings_SkipsRateLimitedAccounts(t *testing.T) {
	accounts := []Account{
		testAccount("limited@example.com", false, true, false), // Rate-limited
		testAccount("available@example.com", false, false, false),
	}

	settings := Settings{SoftLimitEnabled: false}

	result := PickNextWithSettings(accounts, 0, "claude-sonnet-4-5", settings, nil)

	if result.Account == nil {
		t.Fatal("expected to find an account, got nil")
	}
	if result.Account.Email != "available@example.com" {
		t.Errorf("expected available@example.com, got %s", result.Account.Email)
	}
}

func TestPickNextWithSettings_SkipsInvalidAccounts(t *testing.T) {
	accounts := []Account{
		testAccount("invalid@example.com", false, false, true), // Invalid
		testAccount("valid@example.com", false, false, false),
	}

	settings := Settings{SoftLimitEnabled: false}

	result := PickNextWithSettings(accounts, 0, "claude-sonnet-4-5", settings, nil)

	if result.Account == nil {
		t.Fatal("expected to find an account, got nil")
	}
	if result.Account.Email != "valid@example.com" {
		t.Errorf("expected valid@example.com, got %s", result.Account.Email)
	}
}

func TestPickNextWithSettings_ReturnsNilWhenAllUnavailable(t *testing.T) {
	accounts := []Account{
		testAccount("limited@example.com", false, true, false),
		testAccount("invalid@example.com", false, false, true),
	}

	settings := Settings{SoftLimitEnabled: false}

	result := PickNextWithSettings(accounts, 0, "claude-sonnet-4-5", settings, nil)

	if result.Account != nil {
		t.Errorf("expected nil when all accounts unavailable, got %s", result.Account.Email)
	}
}

func TestHasNonSoftLimitedAccounts_ReturnsTrueWhenNonSoftLimitedExists(t *testing.T) {
	accounts := []Account{
		testAccount("soft@example.com", true, false, false),
		testAccount("normal@example.com", false, false, false),
	}

	settings := Settings{SoftLimitEnabled: true, SoftLimitThreshold: 0.20}

	if !HasNonSoftLimitedAccounts(accounts, "claude-sonnet-4-5", settings) {
		t.Error("expected true when non-soft-limited account exists")
	}
}

func TestHasNonSoftLimitedAccounts_ReturnsFalseWhenAllSoftLimited(t *testing.T) {
	accounts := []Account{
		testAccount("soft1@example.com", true, false, false),
		testAccount("soft2@example.com", true, false, false),
	}

	settings := Settings{SoftLimitEnabled: true, SoftLimitThreshold: 0.20}

	if HasNonSoftLimitedAccounts(accounts, "claude-sonnet-4-5", settings) {
		t.Error("expected false when all accounts are soft-limited")
	}
}

func TestHasNonSoftLimitedAccounts_ReturnsTrueWhenSoftLimitsDisabled(t *testing.T) {
	accounts := []Account{
		testAccount("soft@example.com", true, false, false),
	}

	settings := Settings{SoftLimitEnabled: false}

	// When disabled, any available account counts as "non-soft-limited"
	if !HasNonSoftLimitedAccounts(accounts, "claude-sonnet-4-5", settings) {
		t.Error("expected true when soft limits are disabled")
	}
}

func TestGetPreferredAccounts_ReturnsOnlyNonSoftLimited(t *testing.T) {
	accounts := []Account{
		testAccount("soft1@example.com", true, false, false),
		testAccount("normal@example.com", false, false, false),
		testAccount("soft2@example.com", true, false, false),
	}

	settings := Settings{SoftLimitEnabled: true, SoftLimitThreshold: 0.20}

	preferred := GetPreferredAccounts(accounts, "claude-sonnet-4-5", settings)

	if len(preferred) != 1 {
		t.Errorf("expected 1 preferred account, got %d", len(preferred))
	}
	if preferred[0].Email != "normal@example.com" {
		t.Errorf("expected normal@example.com, got %s", preferred[0].Email)
	}
}

func TestGetPreferredAccounts_FallsBackWhenAllSoftLimited(t *testing.T) {
	accounts := []Account{
		testAccount("soft1@example.com", true, false, false),
		testAccount("soft2@example.com", true, false, false),
	}

	settings := Settings{SoftLimitEnabled: true, SoftLimitThreshold: 0.20}

	preferred := GetPreferredAccounts(accounts, "claude-sonnet-4-5", settings)

	// Should fall back to all available accounts
	if len(preferred) != 2 {
		t.Errorf("expected 2 accounts (fallback), got %d", len(preferred))
	}
}

func TestIsAccountSoftLimited_ChecksModelSpecificStatus(t *testing.T) {
	acc := testAccount("test@example.com", true, false, false)
	settings := Settings{SoftLimitEnabled: true, SoftLimitThreshold: 0.20}

	// Check for the model that is soft-limited
	if !IsAccountSoftLimited(&acc, "claude-sonnet-4-5", settings) {
		t.Error("expected account to be soft-limited for claude-sonnet-4-5")
	}

	// Check for a different model (not soft-limited)
	if IsAccountSoftLimited(&acc, "gemini-3-flash", settings) {
		t.Error("expected account to not be soft-limited for gemini-3-flash")
	}
}

func TestGetSoftLimitedCount_CountsCorrectly(t *testing.T) {
	accounts := []Account{
		testAccount("soft1@example.com", true, false, false),
		testAccount("normal@example.com", false, false, false),
		testAccount("soft2@example.com", true, false, false),
		testAccount("invalid@example.com", true, false, true), // Invalid accounts shouldn't count
	}

	settings := Settings{SoftLimitEnabled: true, SoftLimitThreshold: 0.20}

	count := GetSoftLimitedCount(accounts, "claude-sonnet-4-5", settings)

	if count != 2 {
		t.Errorf("expected 2 soft-limited accounts, got %d", count)
	}
}

func TestGetSoftLimitedCount_ReturnsZeroWhenDisabled(t *testing.T) {
	accounts := []Account{
		testAccount("soft@example.com", true, false, false),
	}

	settings := Settings{SoftLimitEnabled: false}

	count := GetSoftLimitedCount(accounts, "claude-sonnet-4-5", settings)

	if count != 0 {
		t.Errorf("expected 0 when soft limits disabled, got %d", count)
	}
}
