// Package utils provides shared utilities for the proxy.
package utils

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
)

// Logger wraps slog with colored output and debug mode support.
type Logger struct {
	mu           sync.RWMutex
	debugEnabled bool
	handler      slog.Handler
	logger       *slog.Logger
}

// coloredHandler implements slog.Handler with colored output.
type coloredHandler struct {
	out          io.Writer
	debugEnabled *bool
	mu           *sync.RWMutex
}

func (h *coloredHandler) Enabled(_ context.Context, level slog.Level) bool {
	if level == slog.LevelDebug {
		h.mu.RLock()
		defer h.mu.RUnlock()
		return *h.debugEnabled
	}
	return true
}

func (h *coloredHandler) Handle(_ context.Context, r slog.Record) error {
	var color string
	var prefix string

	switch r.Level {
	case slog.LevelDebug:
		color = colorMagenta
		prefix = "[DEBUG]"
	case slog.LevelInfo:
		color = colorBlue
		prefix = "[INFO]"
	case slog.LevelWarn:
		color = colorYellow
		prefix = "[WARN]"
	case slog.LevelError:
		color = colorRed
		prefix = "[ERROR]"
	default:
		color = colorReset
		prefix = "[LOG]"
	}

	timestamp := r.Time.Format("15:04:05")
	msg := fmt.Sprintf("%s%s %s%s %s\n", color, timestamp, prefix, colorReset, r.Message)

	_, err := h.out.Write([]byte(msg))
	return err
}

func (h *coloredHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h // Simplified: attributes are ignored for colored output
}

func (h *coloredHandler) WithGroup(name string) slog.Handler {
	return h // Simplified: groups are ignored for colored output
}

// NewLogger creates a new Logger instance.
func NewLogger() *Logger {
	l := &Logger{
		debugEnabled: false,
	}

	l.handler = &coloredHandler{
		out:          os.Stdout,
		debugEnabled: &l.debugEnabled,
		mu:           &l.mu,
	}

	l.logger = slog.New(l.handler)
	return l
}

// SetDebug enables or disables debug mode.
func (l *Logger) SetDebug(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debugEnabled = enabled
}

// IsDebugEnabled returns true if debug mode is enabled.
func (l *Logger) IsDebugEnabled() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.debugEnabled
}

// Debug logs a debug message (only if debug mode is enabled).
func (l *Logger) Debug(msg string, args ...any) {
	l.logger.Debug(fmt.Sprintf(msg, args...))
}

// Info logs an info message.
func (l *Logger) Info(msg string, args ...any) {
	l.logger.Info(fmt.Sprintf(msg, args...))
}

// Warn logs a warning message.
func (l *Logger) Warn(msg string, args ...any) {
	l.logger.Warn(fmt.Sprintf(msg, args...))
}

// Error logs an error message.
func (l *Logger) Error(msg string, args ...any) {
	l.logger.Error(fmt.Sprintf(msg, args...))
}

// Success logs a success message (green colored info).
func (l *Logger) Success(msg string, args ...any) {
	timestamp := time.Now().Format("15:04:05")
	formatted := fmt.Sprintf(msg, args...)
	fmt.Printf("%s%s [SUCCESS]%s %s\n", colorGreen, timestamp, colorReset, formatted)
}

// DefaultLogger is the package-level logger instance.
var DefaultLogger = NewLogger()

// SetDebug sets the debug mode on the default logger.
func SetDebug(enabled bool) {
	DefaultLogger.SetDebug(enabled)
}

// IsDebugEnabled returns true if debug mode is enabled on the default logger.
func IsDebugEnabled() bool {
	return DefaultLogger.IsDebugEnabled()
}

// Debug logs using the default logger.
func Debug(msg string, args ...any) {
	DefaultLogger.Debug(msg, args...)
}

// Info logs using the default logger.
func Info(msg string, args ...any) {
	DefaultLogger.Info(msg, args...)
}

// Warn logs using the default logger.
func Warn(msg string, args ...any) {
	DefaultLogger.Warn(msg, args...)
}

// Error logs using the default logger.
func Error(msg string, args ...any) {
	DefaultLogger.Error(msg, args...)
}

// Success logs using the default logger.
func Success(msg string, args ...any) {
	DefaultLogger.Success(msg, args...)
}

// FormatDuration formats a duration in human-readable format (e.g., "1h23m45s").
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh%dm%ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// Sleep pauses execution for the specified duration.
func Sleep(d time.Duration) {
	time.Sleep(d)
}
