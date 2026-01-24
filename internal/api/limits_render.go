package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/account"
)

// Table column widths for account status table.
const (
	accColWidth      = 25
	statusColWidth   = 15
	lastUsedColWidth = 25
	resetColWidth    = 25
	accountColWidth  = 30
)

// accountStats holds summary statistics for accounts.
type accountStats struct {
	total       int
	invalid     int
	rateLimited int
	available   int
}

// renderAccountLimitsJSON formats account limits data for JSON response.
func renderAccountLimitsJSON(sortedModels []string, accountLimits []map[string]interface{}) []map[string]interface{} {
	accounts := make([]map[string]interface{}, 0, len(accountLimits))
	for _, acc := range accountLimits {
		email, _ := acc["email"].(string)
		status, _ := acc["status"].(string)
		provider, _ := acc["provider"].(string)
		errStr, _ := acc["error"].(string)
		if errStr == "" {
			acc["error"] = nil
		}

		models, _ := acc["models"].(map[string]interface{})

		// Only include models that belong to this provider
		providerModels := filterModelsForProvider(sortedModels, provider)
		limits := make(map[string]interface{}, len(providerModels))
		for _, modelID := range providerModels {
			quotaVal, ok := models[modelID]
			if !ok {
				continue
			}
			quota, _ := quotaVal.(map[string]interface{})
			if quota == nil {
				continue
			}

			rf := quota["remainingFraction"]
			rt := quota["resetTime"]

			remaining := "N/A"
			if rf != nil {
				if f, ok := rf.(float64); ok {
					remaining = fmt.Sprintf("%d%%", int64(f*100+0.5))
				}
			}

			limits[modelID] = map[string]interface{}{
				"remaining":         remaining,
				"remainingFraction": rf,
				"resetTime":         rt,
			}
		}

		accounts = append(accounts, map[string]interface{}{
			"email":  email,
			"status": status,
			"error":  acc["error"],
			"limits": limits,
		})
	}
	return accounts
}

// renderAccountLimitsTable formats account limits data as a text table.
func renderAccountLimitsTable(now time.Time, allAccounts []account.Account, accountLimits []map[string]interface{}, sortedModels []string) string {
	lines := make([]string, 0, 64)

	// Header with timestamp
	timestamp := now.In(time.Local).Format("1/2/2006, 3:04:05 PM")
	lines = append(lines, fmt.Sprintf("Account Limits (%s)", timestamp))

	// Summary line
	stats := countAccountStats(allAccounts, now.UnixMilli())
	lines = append(lines, fmt.Sprintf("Accounts: %d total, %d available, %d rate-limited, %d invalid",
		stats.total, stats.available, stats.rateLimited, stats.invalid))
	lines = append(lines, "")

	// Table 1: Account status
	lines = append(lines, renderAccountStatusTable(allAccounts, accountLimits, sortedModels)...)
	lines = append(lines, "")

	// Table 2: Model quotas
	lines = append(lines, renderModelQuotaTable(now, accountLimits, sortedModels)...)

	return strings.Join(lines, "\n")
}

// countAccountStats counts account statistics (total, invalid, rate-limited).
func countAccountStats(allAccounts []account.Account, nowMs int64) accountStats {
	stats := accountStats{total: len(allAccounts)}

	for _, acc := range allAccounts {
		if acc.IsInvalid {
			stats.invalid++
			continue
		}

		for _, limit := range acc.ModelRateLimits {
			if limit.IsRateLimited && limit.ResetTime > nowMs {
				stats.rateLimited++
				break
			}
		}
	}

	stats.available = stats.total - stats.invalid
	return stats
}

// renderAccountStatusTable renders the account status table rows.
func renderAccountStatusTable(allAccounts []account.Account, accountLimits []map[string]interface{}, sortedModels []string) []string {
	lines := make([]string, 0, len(allAccounts)+2)

	// Header
	lines = append(lines, padRight("Account", accColWidth)+padRight("Status", statusColWidth)+padRight("Last Used", lastUsedColWidth)+"Quota Reset")
	lines = append(lines, strings.Repeat("─", accColWidth+statusColWidth+lastUsedColWidth+resetColWidth))

	// Find first Claude model for reset time display
	claudeModel := findFirstClaudeModel(sortedModels)

	for _, acc := range allAccounts {
		shortEmail := truncateEmail(acc.Email, 22)
		lastUsed := formatLastUsed(acc.LastUsed)
		accLimit := findAccountLimits(accountLimits, acc.Email)
		accStatus := getAccountStatus(acc, accLimit)
		resetTime := getResetTime(claudeModel, accLimit)

		row := padRight(shortEmail, accColWidth) + padRight(accStatus, statusColWidth) + padRight(lastUsed, lastUsedColWidth) + resetTime

		// Add error detail if present
		if accLimit != nil {
			if errStr, _ := (*accLimit)["error"].(string); errStr != "" {
				lines = append(lines, row)
				lines = append(lines, "  └─ "+errStr)
				continue
			}
		}
		lines = append(lines, row)
	}

	return lines
}

// getAccountStatus determines the status string for an account.
func getAccountStatus(acc account.Account, accLimit *map[string]interface{}) string {
	if acc.IsInvalid {
		return "invalid"
	}

	if accLimit == nil {
		return "ok"
	}

	if st, _ := (*accLimit)["status"].(string); st == "error" {
		return "error"
	}

	models, _ := (*accLimit)["models"].(map[string]interface{})
	modelCount := len(models)
	exhaustedCount := 0

	for _, qv := range models {
		q, _ := qv.(map[string]interface{})
		if q == nil {
			continue
		}
		rf := q["remainingFraction"]
		if rf == nil {
			exhaustedCount++
			continue
		}
		if f, ok := rf.(float64); ok && f == 0 {
			exhaustedCount++
		}
	}

	if exhaustedCount == 0 {
		return "ok"
	}
	return fmt.Sprintf("(%d/%d) limited", exhaustedCount, modelCount)
}

// getResetTime extracts the reset time for a model from account limits.
func getResetTime(modelID string, accLimit *map[string]interface{}) string {
	if modelID == "" || accLimit == nil {
		return "-"
	}

	models, _ := (*accLimit)["models"].(map[string]interface{})
	qv, ok := models[modelID]
	if !ok {
		return "-"
	}

	q, _ := qv.(map[string]interface{})
	if q == nil {
		return "-"
	}

	if rt, ok := q["resetTime"].(string); ok && rt != "" {
		return formatLocaleTime(rt)
	}
	return "-"
}

// renderModelQuotaTable renders the model quota table rows.
func renderModelQuotaTable(now time.Time, accountLimits []map[string]interface{}, sortedModels []string) []string {
	lines := make([]string, 0, len(sortedModels)+2)

	// Calculate model column width
	modelColWidth := 28
	for _, m := range sortedModels {
		if l := len(m); l > modelColWidth {
			modelColWidth = l
		}
	}
	modelColWidth += 2

	// Header
	header := padRight("Model", modelColWidth)
	for _, acc := range accountLimits {
		email, _ := acc["email"].(string)
		shortEmail := truncateEmail(email, 26)
		header += padRight(shortEmail, accountColWidth)
	}
	lines = append(lines, header)
	lines = append(lines, strings.Repeat("─", modelColWidth+len(accountLimits)*accountColWidth))

	// Model rows
	for _, modelID := range sortedModels {
		row := padRight(modelID, modelColWidth)
		for _, acc := range accountLimits {
			cell := formatQuotaCell(now, modelID, acc)
			row += padRight(cell, accountColWidth)
		}
		lines = append(lines, row)
	}

	return lines
}

// formatQuotaCell formats a single quota cell for the model table.
func formatQuotaCell(now time.Time, modelID string, acc map[string]interface{}) string {
	status, _ := acc["status"].(string)
	provider, _ := acc["provider"].(string)
	models, _ := acc["models"].(map[string]interface{})

	// Skip models that don't belong to this provider
	if !hasProviderPrefix(modelID, provider) {
		return "-"
	}

	if status != "ok" && status != "rate-limited" {
		return fmt.Sprintf("[%s]", status)
	}

	qv, ok := models[modelID]
	if !ok {
		return "-"
	}

	q, _ := qv.(map[string]interface{})
	if q == nil {
		return "-"
	}

	rf := q["remainingFraction"]
	rt, _ := q["resetTime"].(string)

	// Use safe type assertion to avoid panic on unexpected types
	rfFloat, rfOk := rf.(float64)
	if rf == nil || (rfOk && rfFloat == 0) {
		if rt != "" {
			resetMs := parseResetMs(now, rt)
			if resetMs > 0 {
				return fmt.Sprintf("0%% (wait %s)", formatDurationMs(resetMs))
			}
			return "0% (resetting...)"
		}
		return "0% (exhausted)"
	}

	if rfOk {
		return fmt.Sprintf("%d%%", int64(rfFloat*100+0.5))
	}
	return "-"
}

// truncateEmail extracts and truncates the email prefix (before @).
func truncateEmail(email string, maxLen int) string {
	shortEmail := strings.Split(email, "@")[0]
	if len(shortEmail) > maxLen {
		return shortEmail[:maxLen]
	}
	return shortEmail
}

// formatLastUsed formats the last used time, returning "never" if nil.
func formatLastUsed(lastUsed *time.Time) string {
	if lastUsed == nil {
		return "never"
	}
	return lastUsed.In(time.Local).Format("1/2/2006, 3:04:05 PM")
}

// findFirstClaudeModel finds the first model containing "claude" in the list.
func findFirstClaudeModel(models []string) string {
	for _, m := range models {
		if strings.Contains(m, "claude") {
			return m
		}
	}
	return ""
}

// findAccountLimits finds the account limits entry for the given email.
func findAccountLimits(accountLimits []map[string]interface{}, email string) *map[string]interface{} {
	for i := range accountLimits {
		if e, _ := accountLimits[i]["email"].(string); e == email {
			return &accountLimits[i]
		}
	}
	return nil
}

// padRight pads a string to the specified width with spaces.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// formatLocaleTime formats an ISO timestamp to locale time format.
func formatLocaleTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "Invalid Date"
	}
	return t.In(time.Local).Format("1/2/2006, 3:04:05 PM")
}

// formatISOTimeUTC formats a time to ISO format in UTC.
func formatISOTimeUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// parseResetMs calculates milliseconds until reset from an ISO timestamp.
func parseResetMs(now time.Time, iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}
	return t.UnixMilli() - now.UnixMilli()
}

// formatDurationMs formats a duration in milliseconds to a human-readable string.
func formatDurationMs(ms int64) string {
	seconds := ms / 1000
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60

	if hours > 0 {
		return strconv.FormatInt(hours, 10) + "h" + strconv.FormatInt(minutes, 10) + "m" + strconv.FormatInt(secs, 10) + "s"
	}
	if minutes > 0 {
		return strconv.FormatInt(minutes, 10) + "m" + strconv.FormatInt(secs, 10) + "s"
	}
	return strconv.FormatInt(secs, 10) + "s"
}

// hasProviderPrefix checks if a model ID has the specified provider prefix.
// Model IDs are in the format "provider/model-name".
func hasProviderPrefix(modelID, provider string) bool {
	if modelID == "" || provider == "" {
		return false
	}
	return strings.HasPrefix(modelID, provider+"/")
}

// filterModelsForProvider filters a list of model IDs to only include those
// belonging to the specified provider.
func filterModelsForProvider(models []string, provider string) []string {
	result := make([]string, 0, len(models))
	for _, m := range models {
		if hasProviderPrefix(m, provider) {
			result = append(result, m)
		}
	}
	return result
}
