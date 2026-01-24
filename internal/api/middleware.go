package api

import (
	"net/http"
	"time"

	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
)

// ConfigurableCORS adds CORS headers based on environment configuration.
// Uses CORS_ENABLED, CORS_ALLOW_ORIGIN, CORS_ALLOW_METHODS, CORS_ALLOW_HEADERS, CORS_MAX_AGE env vars.
//
// Security: By default CORS_ALLOW_ORIGIN is "*" for development convenience.
// For production, set CORS_ALLOW_ORIGIN to specific origins (e.g., "https://example.com").
func ConfigurableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corsConfig := config.GetCORSConfig()

		// Skip CORS if disabled
		if !corsConfig.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", corsConfig.AllowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", corsConfig.AllowMethods)
		w.Header().Set("Access-Control-Allow-Headers", corsConfig.AllowHeaders)
		w.Header().Set("Access-Control-Max-Age", corsConfig.MaxAge)

		// Handle preflight
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Logger logs incoming requests and their duration.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response wrapper to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		// Skip logging for health checks in non-debug mode
		if r.URL.Path == "/health" && !utils.IsDebugEnabled() {
			return
		}

		utils.Info("[%s] %s %s %d %s",
			r.Method,
			r.URL.Path,
			r.RemoteAddr,
			rw.statusCode,
			formatDuration(duration))
	})
}

// Recovery recovers from panics and returns a 500 error.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				utils.Error("[Panic] %v", err)
				http.Error(w, `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	if d < time.Second {
		return d.Truncate(time.Millisecond).String()
	}
	return d.Truncate(time.Millisecond).String()
}
