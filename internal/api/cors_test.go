package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestConfigurableCORS(t *testing.T) {
	// Save and restore env vars
	envVars := []string{"CORS_ENABLED", "CORS_ALLOW_ORIGIN", "CORS_ALLOW_METHODS", "CORS_ALLOW_HEADERS", "CORS_MAX_AGE"}
	savedVars := make(map[string]string)
	for _, v := range envVars {
		savedVars[v] = os.Getenv(v)
	}
	defer func() {
		for _, v := range envVars {
			if savedVars[v] != "" {
				os.Setenv(v, savedVars[v])
			} else {
				os.Unsetenv(v)
			}
		}
	}()

	// Clear all CORS env vars
	for _, v := range envVars {
		os.Unsetenv(v)
	}

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	t.Run("default CORS headers applied", func(t *testing.T) {
		handler := ConfigurableCORS(nextHandler)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if origin := rr.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
			t.Errorf("Allow-Origin = %q, want %q", origin, "*")
		}
		if methods := rr.Header().Get("Access-Control-Allow-Methods"); methods != "GET, POST, PUT, DELETE, OPTIONS" {
			t.Errorf("Allow-Methods = %q, want %q", methods, "GET, POST, PUT, DELETE, OPTIONS")
		}
	})

	t.Run("custom CORS origin from env", func(t *testing.T) {
		os.Setenv("CORS_ALLOW_ORIGIN", "https://example.com")
		defer os.Unsetenv("CORS_ALLOW_ORIGIN")

		handler := ConfigurableCORS(nextHandler)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if origin := rr.Header().Get("Access-Control-Allow-Origin"); origin != "https://example.com" {
			t.Errorf("Allow-Origin = %q, want %q", origin, "https://example.com")
		}
	})

	t.Run("CORS disabled", func(t *testing.T) {
		os.Setenv("CORS_ENABLED", "false")
		defer os.Unsetenv("CORS_ENABLED")

		handler := ConfigurableCORS(nextHandler)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if origin := rr.Header().Get("Access-Control-Allow-Origin"); origin != "" {
			t.Errorf("CORS headers should not be set when disabled, got Allow-Origin = %q", origin)
		}
	})

	t.Run("preflight request handled", func(t *testing.T) {
		handler := ConfigurableCORS(nextHandler)
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("preflight status = %d, want %d", rr.Code, http.StatusOK)
		}

		// Body should be empty for preflight
		if body := rr.Body.String(); body != "" {
			t.Errorf("preflight body = %q, want empty", body)
		}
	})

	t.Run("preflight not handled when CORS disabled", func(t *testing.T) {
		os.Setenv("CORS_ENABLED", "false")
		defer os.Unsetenv("CORS_ENABLED")

		handler := ConfigurableCORS(nextHandler)
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		// Should pass through to next handler when CORS is disabled
		if rr.Body.String() != "OK" {
			t.Errorf("body = %q, want %q", rr.Body.String(), "OK")
		}
	})

	t.Run("custom headers from env", func(t *testing.T) {
		os.Setenv("CORS_ALLOW_HEADERS", "Content-Type, X-Custom-Header")
		defer os.Unsetenv("CORS_ALLOW_HEADERS")

		handler := ConfigurableCORS(nextHandler)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if headers := rr.Header().Get("Access-Control-Allow-Headers"); headers != "Content-Type, X-Custom-Header" {
			t.Errorf("Allow-Headers = %q, want %q", headers, "Content-Type, X-Custom-Header")
		}
	})
}
