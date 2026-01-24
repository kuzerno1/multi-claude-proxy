// Package config contains configuration constants for the multi-claude-proxy.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Server configuration
const (
	DefaultPort      = 8080
	RequestBodyLimit = 50 * 1024 * 1024 // 50MB
)

// Retry and timeout configuration
const (
	MaxRetries              = 5  // Max retry attempts across accounts
	MaxEmptyResponseRetries = 2  // Max retries for empty API responses
	MaxAccounts             = 10 // Maximum number of accounts allowed
	DefaultCooldownDuration = 10 * time.Second
	MaxWaitBeforeError      = 2 * time.Minute // Throw error if wait exceeds this
	TokenRefreshInterval    = 5 * time.Minute

	// Post-wait buffer times
	PostRateLimitBuffer = 500 * time.Millisecond // Buffer after rate limit wait
	NetworkRetryDelay   = 1 * time.Second        // Delay between network error retries

	// Default rate limit reset time when not specified by API
	DefaultRateLimitResetMs = 60000 // 1 minute in milliseconds
)

// Soft limit configuration
// Soft limits prevent accounts from being drained to 0% quota, avoiding the 7-day reset timer.
// Note: Antigravity reports quota in 20% steps (100%, 80%, 60%, 40%, 20%, 0%).
const (
	DefaultSoftLimitThreshold = 0.20 // 20% - accounts at or below this are considered soft-limited
)

// Thinking model constants
const (
	MinSignatureLength      = 50 // Minimum valid thinking signature length
	GeminiMaxOutputTokens   = 16384
	GeminiSkipSignature     = "skip_thought_signature_validator"
	GeminiSignatureCacheTTL = 2 * time.Hour
)

// Image generation constants
const (
	DefaultImageModel = "gemini-3-pro-image"
	MaxImageCount     = 4
	DefaultImageCount = 1
)

// OAuth configuration
const (
	OAuthCallbackPort = 51121
)

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// OAuthConfig contains Google OAuth 2.0 configuration.
// NOTE: Hard-coded client secret is kept for Node parity.
// TODO: Remove hard-coded client secret and require env-only configuration for production.
var OAuthConfig = struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	CallbackPort int
	Scopes       []string
	RedirectURI  string
}{
	ClientID:     getEnvOrDefault("GOOGLE_CLIENT_ID", "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"),
	ClientSecret: getEnvOrDefault("GOOGLE_CLIENT_SECRET", "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"),
	AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:     "https://oauth2.googleapis.com/token",
	UserInfoURL:  "https://www.googleapis.com/oauth2/v1/userinfo",
	CallbackPort: OAuthCallbackPort,
	Scopes: []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	},
	RedirectURI: fmt.Sprintf("http://localhost:%d/oauth-callback", OAuthCallbackPort),
}

// Z.AI API configuration
const (
	ZAIBaseURL    = "https://api.z.ai/api/anthropic"
	ZAIModelsPath = "/v1/models"
	ZAIQuotaURL   = "https://api.z.ai/api/monitor/usage/quota/limit"
	ZAIAuthHeader = "Authorization"
	ZAITimeout    = 10 * time.Minute // Client-side timeout for Z.AI message requests
)

// Health/Status endpoint timeouts
const (
	QuotaFetchTimeout = 15 * time.Second // Timeout for quota/status fetch operations
)

// Antigravity API configuration
var (
	// AntigravityEndpointFallbacks contains Cloud Code API endpoints in fallback order.
	AntigravityEndpointFallbacks = []string{
		"https://daily-cloudcode-pa.googleapis.com",
		"https://cloudcode-pa.googleapis.com",
	}

	// DefaultProjectID is used if none can be discovered.
	DefaultProjectID = "rising-fact-p41fc"

	// AntigravitySystemInstruction is the minimal system instruction for Antigravity.
	AntigravitySystemInstruction = `You are Antigravity, a powerful agentic AI coding assistant designed by the Google Deepmind team working on Advanced Agentic Coding.You are pair programming with a USER to solve their coding task. The task may require creating a new codebase, modifying or debugging an existing codebase, or simply answering a question.**Absolute paths only****Proactiveness**`
)

// ModelFallbackMap maps primary models to fallback models when quota is exhausted.
var ModelFallbackMap = map[string]string{
	"gemini-3-pro-high":          "claude-opus-4-5-thinking",
	"gemini-3-pro-low":           "claude-sonnet-4-5",
	"gemini-3-flash":             "claude-sonnet-4-5-thinking",
	"claude-opus-4-5-thinking":   "gemini-3-pro-high",
	"claude-sonnet-4-5-thinking": "gemini-3-flash",
	"claude-sonnet-4-5":          "gemini-3-flash",
}

// GetAntigravityHeaders returns the required headers for Antigravity API requests.
func GetAntigravityHeaders() map[string]string {
	return map[string]string{
		"User-Agent":        getPlatformUserAgent(),
		"X-Goog-Api-Client": "google-cloud-sdk vscode_cloudshelleditor/0.1",
		"Client-Metadata":   `{"ideType":"IDE_UNSPECIFIED","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`,
	}
}

// getPlatformUserAgent generates a platform-specific User-Agent string.
func getPlatformUserAgent() string {
	return fmt.Sprintf("antigravity/1.11.5 %s/%s", runtime.GOOS, runtime.GOARCH)
}

// GetAccountConfigPath returns the path to the account configuration file.
// Can be overridden with ACCOUNTS_CONFIG_PATH environment variable.
func GetAccountConfigPath() string {
	if envPath := os.Getenv("ACCOUNTS_CONFIG_PATH"); envPath != "" {
		return envPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config/multi-claude-proxy/accounts.json")
}

// ModelFamily represents the family of a model.
type ModelFamily string

const (
	ModelFamilyClaude  ModelFamily = "claude"
	ModelFamilyGemini  ModelFamily = "gemini"
	ModelFamilyUnknown ModelFamily = "unknown"
)

// GetModelFamily returns the model family from the model name.
func GetModelFamily(modelName string) ModelFamily {
	lower := strings.ToLower(modelName)
	if strings.Contains(lower, "claude") {
		return ModelFamilyClaude
	}
	if strings.Contains(lower, "gemini") {
		return ModelFamilyGemini
	}
	return ModelFamilyUnknown
}

// geminiVersionRegex matches "gemini-X" where X is a version number.
var geminiVersionRegex = regexp.MustCompile(`gemini-(\d+)`)

// IsThinkingModel checks if a model supports thinking/reasoning output.
func IsThinkingModel(modelName string) bool {
	lower := strings.ToLower(modelName)

	// Claude thinking models have "thinking" in the name
	if strings.Contains(lower, "claude") && strings.Contains(lower, "thinking") {
		return true
	}

	// Gemini thinking models: explicit "thinking" in name, OR gemini version 3+ (excluding image models)
	if strings.Contains(lower, "gemini") {
		if strings.Contains(lower, "thinking") {
			return true
		}
		// Image models are not thinking models
		if strings.Contains(lower, "image") {
			return false
		}
		// Check for gemini-3 or higher (e.g., gemini-3, gemini-3.5, gemini-4, etc.)
		matches := geminiVersionRegex.FindStringSubmatch(lower)
		if len(matches) >= 2 {
			version, err := strconv.Atoi(matches[1])
			if err == nil && version >= 3 {
				return true
			}
		}
	}

	return false
}

// GetFallbackModel returns the fallback model for the given model, or empty string if none.
func GetFallbackModel(model string) string {
	return ModelFallbackMap[model]
}

// HasFallback returns true if the model has a fallback configured.
func HasFallback(model string) bool {
	_, ok := ModelFallbackMap[model]
	return ok
}
