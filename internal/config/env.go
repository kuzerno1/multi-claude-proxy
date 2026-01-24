// Package config provides environment variable configuration for the proxy.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEnvInt returns an environment variable as int, or the default value.
func GetEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// GetEnvFloat returns an environment variable as float64, or the default value.
func GetEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// GetEnvBool returns an environment variable as bool, or the default value.
// Accepts: true, 1, yes (case insensitive) as true values.
// Accepts: false, 0, no (case insensitive) as false values.
func GetEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		lower := strings.ToLower(value)
		switch lower {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return defaultValue
}

// GetEnvDuration returns an environment variable as time.Duration, or the default value.
// Accepts Go duration strings like "10s", "5m", "2h".
func GetEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// GetEnvStringSlice returns an environment variable as a slice of strings (comma-separated).
func GetEnvStringSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		parts := strings.Split(value, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	return defaultValue
}

// GetPort returns the server port from PORT env var or default.
func GetPort() int {
	return GetEnvInt("PORT", DefaultPort)
}

// GetBindAddress returns the bind address from BIND_ADDRESS env var or default.
func GetBindAddress() string {
	return getEnvOrDefault("BIND_ADDRESS", "0.0.0.0")
}

// GetProxyAPIKey returns the proxy API key from PROXY_API_KEY env var.
// Returns empty string if not set.
func GetProxyAPIKey() string {
	return os.Getenv("PROXY_API_KEY")
}

// ValidateRequiredEnvVars validates that all required environment variables are set.
// Returns an error if any required variable is missing.
func ValidateRequiredEnvVars() error {
	if GetProxyAPIKey() == "" {
		return fmt.Errorf("PROXY_API_KEY environment variable is required")
	}
	return nil
}

// CORSConfig holds CORS configuration.
type CORSConfig struct {
	Enabled      bool
	AllowOrigin  string
	AllowMethods string
	AllowHeaders string
	MaxAge       string
}

// GetCORSConfig returns the CORS configuration from environment variables.
func GetCORSConfig() CORSConfig {
	return CORSConfig{
		Enabled:      GetEnvBool("CORS_ENABLED", true),
		AllowOrigin:  getEnvOrDefault("CORS_ALLOW_ORIGIN", "*"),
		AllowMethods: getEnvOrDefault("CORS_ALLOW_METHODS", "GET, POST, PUT, DELETE, OPTIONS"),
		AllowHeaders: getEnvOrDefault("CORS_ALLOW_HEADERS", "Content-Type, Authorization, X-API-Key, anthropic-version, x-session-id"),
		MaxAge:       getEnvOrDefault("CORS_MAX_AGE", "86400"),
	}
}

// ServerTimeouts holds server timeout configuration.
type ServerTimeouts struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// GetServerTimeouts returns server timeout configuration from environment variables.
func GetServerTimeouts() ServerTimeouts {
	return ServerTimeouts{
		ReadTimeout:  time.Duration(GetEnvInt("READ_TIMEOUT_SEC", 30)) * time.Second,
		WriteTimeout: time.Duration(GetEnvInt("WRITE_TIMEOUT_SEC", 300)) * time.Second,
		IdleTimeout:  time.Duration(GetEnvInt("IDLE_TIMEOUT_SEC", 120)) * time.Second,
	}
}

// GetEnableFallback returns whether model fallback is enabled.
func GetEnableFallback() bool {
	return GetEnvBool("ENABLE_FALLBACK", false)
}

// GetSoftLimitThreshold returns the soft limit threshold from env or default.
func GetSoftLimitThreshold() float64 {
	return GetEnvFloat("SOFT_LIMIT_THRESHOLD", DefaultSoftLimitThreshold)
}

// GetDebugEnabled returns whether debug mode is enabled.
func GetDebugEnabled() bool {
	return GetEnvBool("DEBUG", false)
}
