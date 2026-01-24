package config

import (
	"os"
	"testing"
	"time"
)

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue int
		expected     int
	}{
		{
			name:         "returns default when env not set",
			envKey:       "TEST_INT_UNSET",
			envValue:     "",
			defaultValue: 42,
			expected:     42,
		},
		{
			name:         "returns parsed int when env is set",
			envKey:       "TEST_INT_SET",
			envValue:     "100",
			defaultValue: 42,
			expected:     100,
		},
		{
			name:         "returns default when env is invalid",
			envKey:       "TEST_INT_INVALID",
			envValue:     "not-a-number",
			defaultValue: 42,
			expected:     42,
		},
		{
			name:         "handles negative numbers",
			envKey:       "TEST_INT_NEGATIVE",
			envValue:     "-5",
			defaultValue: 42,
			expected:     -5,
		},
		{
			name:         "handles zero",
			envKey:       "TEST_INT_ZERO",
			envValue:     "0",
			defaultValue: 42,
			expected:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			result := GetEnvInt(tt.envKey, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvInt(%q, %d) = %d, want %d", tt.envKey, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvFloat(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue float64
		expected     float64
	}{
		{
			name:         "returns default when env not set",
			envKey:       "TEST_FLOAT_UNSET",
			envValue:     "",
			defaultValue: 0.20,
			expected:     0.20,
		},
		{
			name:         "returns parsed float when env is set",
			envKey:       "TEST_FLOAT_SET",
			envValue:     "0.15",
			defaultValue: 0.20,
			expected:     0.15,
		},
		{
			name:         "returns default when env is invalid",
			envKey:       "TEST_FLOAT_INVALID",
			envValue:     "not-a-float",
			defaultValue: 0.20,
			expected:     0.20,
		},
		{
			name:         "handles zero",
			envKey:       "TEST_FLOAT_ZERO",
			envValue:     "0.0",
			defaultValue: 0.20,
			expected:     0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			result := GetEnvFloat(tt.envKey, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvFloat(%q, %f) = %f, want %f", tt.envKey, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue bool
		expected     bool
	}{
		{
			name:         "returns default when env not set",
			envKey:       "TEST_BOOL_UNSET",
			envValue:     "",
			defaultValue: false,
			expected:     false,
		},
		{
			name:         "returns true for 'true'",
			envKey:       "TEST_BOOL_TRUE",
			envValue:     "true",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns true for '1'",
			envKey:       "TEST_BOOL_ONE",
			envValue:     "1",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns true for 'yes'",
			envKey:       "TEST_BOOL_YES",
			envValue:     "yes",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns false for 'false'",
			envKey:       "TEST_BOOL_FALSE",
			envValue:     "false",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns false for '0'",
			envKey:       "TEST_BOOL_ZERO",
			envValue:     "0",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "is case insensitive",
			envKey:       "TEST_BOOL_CASE",
			envValue:     "TRUE",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns default for invalid value",
			envKey:       "TEST_BOOL_INVALID",
			envValue:     "maybe",
			defaultValue: true,
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			result := GetEnvBool(tt.envKey, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvBool(%q, %v) = %v, want %v", tt.envKey, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "returns default when env not set",
			envKey:       "TEST_DUR_UNSET",
			envValue:     "",
			defaultValue: 10 * time.Second,
			expected:     10 * time.Second,
		},
		{
			name:         "parses seconds",
			envKey:       "TEST_DUR_SEC",
			envValue:     "30s",
			defaultValue: 10 * time.Second,
			expected:     30 * time.Second,
		},
		{
			name:         "parses minutes",
			envKey:       "TEST_DUR_MIN",
			envValue:     "5m",
			defaultValue: 10 * time.Second,
			expected:     5 * time.Minute,
		},
		{
			name:         "parses hours",
			envKey:       "TEST_DUR_HOUR",
			envValue:     "2h",
			defaultValue: 10 * time.Second,
			expected:     2 * time.Hour,
		},
		{
			name:         "returns default for invalid value",
			envKey:       "TEST_DUR_INVALID",
			envValue:     "not-a-duration",
			defaultValue: 10 * time.Second,
			expected:     10 * time.Second,
		},
		{
			name:         "parses complex duration",
			envKey:       "TEST_DUR_COMPLEX",
			envValue:     "1h30m",
			defaultValue: 10 * time.Second,
			expected:     90 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			result := GetEnvDuration(tt.envKey, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvDuration(%q, %v) = %v, want %v", tt.envKey, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvStringSlice(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue []string
		expected     []string
	}{
		{
			name:         "returns default when env not set",
			envKey:       "TEST_SLICE_UNSET",
			envValue:     "",
			defaultValue: []string{"a", "b"},
			expected:     []string{"a", "b"},
		},
		{
			name:         "parses comma-separated values",
			envKey:       "TEST_SLICE_CSV",
			envValue:     "x,y,z",
			defaultValue: []string{"a", "b"},
			expected:     []string{"x", "y", "z"},
		},
		{
			name:         "trims whitespace",
			envKey:       "TEST_SLICE_TRIM",
			envValue:     " a , b , c ",
			defaultValue: []string{},
			expected:     []string{"a", "b", "c"},
		},
		{
			name:         "handles single value",
			envKey:       "TEST_SLICE_SINGLE",
			envValue:     "only-one",
			defaultValue: []string{"a", "b"},
			expected:     []string{"only-one"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			result := GetEnvStringSlice(tt.envKey, tt.defaultValue)
			if len(result) != len(tt.expected) {
				t.Errorf("GetEnvStringSlice(%q) length = %d, want %d", tt.envKey, len(result), len(tt.expected))
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("GetEnvStringSlice(%q)[%d] = %q, want %q", tt.envKey, i, result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestGetAccountConfigPath(t *testing.T) {
	// Test that env var overrides default
	t.Run("returns env var when set", func(t *testing.T) {
		customPath := "/custom/path/accounts.json"
		os.Setenv("ACCOUNTS_CONFIG_PATH", customPath)
		defer os.Unsetenv("ACCOUNTS_CONFIG_PATH")

		result := GetAccountConfigPath()
		if result != customPath {
			t.Errorf("GetAccountConfigPath() = %q, want %q", result, customPath)
		}
	})

	t.Run("returns default when env not set", func(t *testing.T) {
		os.Unsetenv("ACCOUNTS_CONFIG_PATH")
		result := GetAccountConfigPath()
		// Should contain the default path pattern
		if result == "" {
			t.Error("GetAccountConfigPath() returned empty string")
		}
	})
}

func TestGetPort(t *testing.T) {
	t.Run("returns default port when env not set", func(t *testing.T) {
		os.Unsetenv("PORT")
		result := GetPort()
		if result != DefaultPort {
			t.Errorf("GetPort() = %d, want %d", result, DefaultPort)
		}
	})

	t.Run("returns env var when set", func(t *testing.T) {
		os.Setenv("PORT", "9000")
		defer os.Unsetenv("PORT")

		result := GetPort()
		if result != 9000 {
			t.Errorf("GetPort() = %d, want 9000", result)
		}
	})
}

func TestGetBindAddress(t *testing.T) {
	t.Run("returns default when env not set", func(t *testing.T) {
		os.Unsetenv("BIND_ADDRESS")
		result := GetBindAddress()
		if result != "0.0.0.0" {
			t.Errorf("GetBindAddress() = %q, want %q", result, "0.0.0.0")
		}
	})

	t.Run("returns env var when set", func(t *testing.T) {
		os.Setenv("BIND_ADDRESS", "127.0.0.1")
		defer os.Unsetenv("BIND_ADDRESS")

		result := GetBindAddress()
		if result != "127.0.0.1" {
			t.Errorf("GetBindAddress() = %q, want %q", result, "127.0.0.1")
		}
	})
}

func TestGetProxyAPIKey(t *testing.T) {
	t.Run("returns empty when env not set", func(t *testing.T) {
		os.Unsetenv("PROXY_API_KEY")
		result := GetProxyAPIKey()
		if result != "" {
			t.Errorf("GetProxyAPIKey() = %q, want empty string", result)
		}
	})

	t.Run("returns env var when set", func(t *testing.T) {
		os.Setenv("PROXY_API_KEY", "test-api-key-123")
		defer os.Unsetenv("PROXY_API_KEY")

		result := GetProxyAPIKey()
		if result != "test-api-key-123" {
			t.Errorf("GetProxyAPIKey() = %q, want %q", result, "test-api-key-123")
		}
	})
}

func TestValidateRequiredEnvVars(t *testing.T) {
	t.Run("returns error when PROXY_API_KEY not set", func(t *testing.T) {
		os.Unsetenv("PROXY_API_KEY")
		err := ValidateRequiredEnvVars()
		if err == nil {
			t.Error("ValidateRequiredEnvVars() returned nil, want error")
		}
	})

	t.Run("returns nil when PROXY_API_KEY is set", func(t *testing.T) {
		os.Setenv("PROXY_API_KEY", "some-key")
		defer os.Unsetenv("PROXY_API_KEY")

		err := ValidateRequiredEnvVars()
		if err != nil {
			t.Errorf("ValidateRequiredEnvVars() returned error: %v", err)
		}
	})
}

func TestGetCORSConfig(t *testing.T) {
	t.Run("returns defaults when env not set", func(t *testing.T) {
		os.Unsetenv("CORS_ENABLED")
		os.Unsetenv("CORS_ALLOW_ORIGIN")

		cfg := GetCORSConfig()
		if !cfg.Enabled {
			t.Error("CORS should be enabled by default")
		}
		if cfg.AllowOrigin != "*" {
			t.Errorf("AllowOrigin = %q, want %q", cfg.AllowOrigin, "*")
		}
	})

	t.Run("returns env values when set", func(t *testing.T) {
		os.Setenv("CORS_ENABLED", "false")
		os.Setenv("CORS_ALLOW_ORIGIN", "https://example.com")
		defer os.Unsetenv("CORS_ENABLED")
		defer os.Unsetenv("CORS_ALLOW_ORIGIN")

		cfg := GetCORSConfig()
		if cfg.Enabled {
			t.Error("CORS should be disabled")
		}
		if cfg.AllowOrigin != "https://example.com" {
			t.Errorf("AllowOrigin = %q, want %q", cfg.AllowOrigin, "https://example.com")
		}
	})
}

func TestGetServerTimeouts(t *testing.T) {
	t.Run("returns defaults when env not set", func(t *testing.T) {
		os.Unsetenv("READ_TIMEOUT_SEC")
		os.Unsetenv("WRITE_TIMEOUT_SEC")
		os.Unsetenv("IDLE_TIMEOUT_SEC")

		timeouts := GetServerTimeouts()
		if timeouts.ReadTimeout != 30*time.Second {
			t.Errorf("ReadTimeout = %v, want %v", timeouts.ReadTimeout, 30*time.Second)
		}
		if timeouts.WriteTimeout != 5*time.Minute {
			t.Errorf("WriteTimeout = %v, want %v", timeouts.WriteTimeout, 5*time.Minute)
		}
		if timeouts.IdleTimeout != 120*time.Second {
			t.Errorf("IdleTimeout = %v, want %v", timeouts.IdleTimeout, 120*time.Second)
		}
	})

	t.Run("returns env values when set", func(t *testing.T) {
		os.Setenv("READ_TIMEOUT_SEC", "60")
		os.Setenv("WRITE_TIMEOUT_SEC", "600")
		os.Setenv("IDLE_TIMEOUT_SEC", "240")
		defer os.Unsetenv("READ_TIMEOUT_SEC")
		defer os.Unsetenv("WRITE_TIMEOUT_SEC")
		defer os.Unsetenv("IDLE_TIMEOUT_SEC")

		timeouts := GetServerTimeouts()
		if timeouts.ReadTimeout != 60*time.Second {
			t.Errorf("ReadTimeout = %v, want %v", timeouts.ReadTimeout, 60*time.Second)
		}
		if timeouts.WriteTimeout != 600*time.Second {
			t.Errorf("WriteTimeout = %v, want %v", timeouts.WriteTimeout, 600*time.Second)
		}
		if timeouts.IdleTimeout != 240*time.Second {
			t.Errorf("IdleTimeout = %v, want %v", timeouts.IdleTimeout, 240*time.Second)
		}
	})
}
