// Package validate checks OpenVPN profiles in four clearly separated layers:
//
//   - Static: file existence/readability, our own parser, referenced files,
//     obvious mistakes. No external programs.
//   - Parser: the real openvpn binary parses the configuration
//     (`--test-crypto`, which never opens a tunnel or contacts a server).
//   - Crypto: certificates and keys are decoded with crypto/x509 (OpenVPN's
//     test mode does not do this).
//   - Credentials: auth-user-pass requirements and credential file state.
//
// Runtime connectivity is not validated here; it is observed while connected.
package validate

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Severity of a check result.
type Severity int

// Severities, ordered.
const (
	OK Severity = iota
	Info
	Warn
	Error
	Skipped
)

func (s Severity) String() string {
	switch s {
	case OK:
		return "OK"
	case Info:
		return "INFO"
	case Warn:
		return "WARNING"
	case Error:
		return "ERROR"
	case Skipped:
		return "SKIPPED"
	}
	return "?"
}

// Layer identifies the validation layer.
type Layer string

// Layers.
const (
	LayerStatic      Layer = "static"
	LayerParser      Layer = "openvpn"
	LayerCrypto      Layer = "crypto"
	LayerCredentials Layer = "credentials"
)

// Layers lists all layers in display order.
var Layers = []Layer{LayerStatic, LayerParser, LayerCrypto, LayerCredentials}

// Check is a single finding.
type Check struct {
	Layer    Layer
	Severity Severity
	Title    string
	Detail   string
	Line     int
}

func (c Check) String() string {
	s := fmt.Sprintf("[%s] %s: %s", c.Layer, c.Severity, c.Title)
	if c.Line > 0 {
		s += fmt.Sprintf(" (line %d)", c.Line)
	}
	if c.Detail != "" {
		s += " — " + c.Detail
	}
	return s
}

// Report is the result of validating one profile.
type Report struct {
	Profile string
	At      time.Time
	Checks  []Check
}

func (r *Report) add(l Layer, sev Severity, title, detail string, line int) {
	r.Checks = append(r.Checks, Check{Layer: l, Severity: sev, Title: title, Detail: detail, Line: line})
}

// Worst returns the most severe result of a layer (Skipped counts lowest
// unless the layer only contains skipped checks).
func (r Report) Worst(l Layer) Severity {
	worst := Severity(-1)
	skipped := false
	for _, c := range r.Checks {
		if c.Layer != l {
			continue
		}
		if c.Severity == Skipped {
			skipped = true
			continue
		}
		worst = max(worst, c.Severity)
	}
	if worst < 0 {
		if skipped {
			return Skipped
		}
		return OK
	}
	return worst
}

// Valid reports whether no check failed with Error.
func (r Report) Valid() bool {
	return !slices.ContainsFunc(r.Checks, func(c Check) bool { return c.Severity == Error })
}

// Errors returns the failing checks.
func (r Report) Errors() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Severity == Error {
			out = append(out, c)
		}
	}
	return out
}

// Summary returns a one-line description.
func (r Report) Summary() string {
	var e, w int
	for _, c := range r.Checks {
		switch c.Severity {
		case Error:
			e++
		case Warn:
			w++
		}
	}
	switch {
	case e > 0:
		return fmt.Sprintf("INVALID (%d error(s), %d warning(s))", e, w)
	case w > 0:
		return fmt.Sprintf("VALID with %d warning(s)", w)
	default:
		return "VALID"
	}
}

// FirstError returns a short explanation of the first error, or "".
func (r Report) FirstError() string {
	for _, c := range r.Checks {
		if c.Severity == Error {
			if c.Detail != "" {
				return c.Title + ": " + c.Detail
			}
			return c.Title
		}
	}
	return ""
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func trimLines(s string, max int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > max {
		lines = append(lines[:max], "…")
	}
	return strings.Join(lines, "\n")
}
