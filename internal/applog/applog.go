// Package applog configures the application's structured log (log/slog)
// with secret redaction and simple size-based rotation.
package applog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// sensitiveKeys are attribute keys whose values are always redacted.
var sensitiveKeys = []string{"password", "passwd", "secret", "token", "credential", "auth_token", "passphrase"}

// Redacted replaces secret values in logs.
const Redacted = "[REDACTED]"

// mgmtSecret matches management commands carrying secrets, e.g.
// `password "Auth" "hunter2"` or `username "Auth" "bob"`.
var mgmtSecret = regexp.MustCompile(`(?i)\b(password|username|needok|needstr|challenge-response)(\s+"[^"]*")\s+"(?:[^"\\]|\\.)*"`)

// RedactString removes secrets from free-form text such as management
// commands or error messages.
func RedactString(s string) string {
	return mgmtSecret.ReplaceAllString(s, `$1$2 "`+Redacted+`"`)
}

func isSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// replaceAttr redacts sensitive attributes and scrubs string values.
func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if isSensitive(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(RedactString(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(RedactString(err.Error()))
		}
	}
	return a
}

// ParseLevel converts a config level string to slog.Level.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewHandler returns a text handler writing to w with redaction enabled.
func NewHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: replaceAttr})
}

// Open opens (rotating if larger than maxSizeMB) the log file and returns a
// logger plus a closer. On failure it returns a logger that discards output
// and the error, so the application can still run.
func Open(path, level string, maxSizeMB int) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Discard(), io.NopCloser(nil), fmt.Errorf("create log directory: %w", err)
	}
	if fi, err := os.Stat(path); err == nil && maxSizeMB > 0 && fi.Size() > int64(maxSizeMB)<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return Discard(), io.NopCloser(nil), fmt.Errorf("open log file: %w", err)
	}
	return slog.New(NewHandler(f, ParseLevel(level))), f, nil
}

// Discard returns a logger that drops everything.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }
