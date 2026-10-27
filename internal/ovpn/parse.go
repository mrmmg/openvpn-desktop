// Package ovpn parses OpenVPN configuration files into directives.
//
// The parser mirrors OpenVPN's tokenizer closely enough for analysis
// (quoting, escapes, comments, inline <tag> blocks, optional leading "--")
// but it is NOT a validator: authoritative syntax checking is delegated to
// the openvpn binary (see package validate).
package ovpn

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

// Directive is one option from a config file.
type Directive struct {
	Name string
	Args []string
	Line int
	// Inline is true for <name>...</name> blocks; Content holds the body.
	Inline  bool
	Content string
}

// Arg returns argument i or "" if absent.
func (d Directive) Arg(i int) string {
	if i < len(d.Args) {
		return d.Args[i]
	}
	return ""
}

// String renders the directive in config syntax (inline content elided).
func (d Directive) String() string {
	if d.Inline {
		return "<" + d.Name + ">…</" + d.Name + ">"
	}
	if len(d.Args) == 0 {
		return d.Name
	}
	parts := make([]string, len(d.Args))
	for i, a := range d.Args {
		if a == "" || strings.ContainsAny(a, " \t\"'#;") {
			a = `"` + strings.ReplaceAll(strings.ReplaceAll(a, `\`, `\\`), `"`, `\"`) + `"`
		}
		parts[i] = a
	}
	return d.Name + " " + strings.Join(parts, " ")
}

// ParseError is a recoverable syntax problem found while parsing.
type ParseError struct {
	Line int
	Msg  string
}

func (e ParseError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// Config is a parsed configuration file.
type Config struct {
	Directives []Directive
	Errors     []ParseError
}

// MaxFileSize bounds the size of files we are willing to parse.
const MaxFileSize = 4 << 20

// ParseFile reads and parses a file.
func ParseFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(io.LimitReader(f, MaxFileSize))
}

// Parse parses configuration text. It only returns an error on I/O failure;
// syntax problems are collected in Config.Errors.
func Parse(r io.Reader) (*Config, error) {
	cfg := &Config{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	lineNo := 0
	var block *Directive
	var body bytes.Buffer
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		if lineNo == 1 {
			raw = strings.TrimPrefix(raw, "\uFEFF")
		}
		trimmed := strings.TrimSpace(raw)
		if block != nil {
			if trimmed == "</"+block.Name+">" {
				block.Content = body.String()
				cfg.Directives = append(cfg.Directives, *block)
				block = nil
				body.Reset()
				continue
			}
			body.WriteString(raw)
			body.WriteByte('\n')
			continue
		}
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && !strings.HasPrefix(trimmed, "</") {
			name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			if name == "" || strings.ContainsAny(name, " \t<>") {
				cfg.Errors = append(cfg.Errors, ParseError{lineNo, "malformed inline block tag " + trimmed})
				continue
			}
			block = &Directive{Name: name, Line: lineNo, Inline: true}
			continue
		}
		if strings.HasPrefix(trimmed, "</") {
			cfg.Errors = append(cfg.Errors, ParseError{lineNo, "unexpected closing tag " + trimmed})
			continue
		}
		tokens, err := tokenize(raw)
		if err != nil {
			cfg.Errors = append(cfg.Errors, ParseError{lineNo, err.Error()})
		}
		if len(tokens) == 0 {
			continue
		}
		name := strings.TrimPrefix(tokens[0], "--")
		cfg.Directives = append(cfg.Directives, Directive{Name: name, Args: tokens[1:], Line: lineNo})
	}
	if err := sc.Err(); err != nil {
		return cfg, err
	}
	if block != nil {
		cfg.Errors = append(cfg.Errors, ParseError{block.Line, "inline block <" + block.Name + "> is not closed"})
	}
	return cfg, nil
}

// tokenize splits a line following OpenVPN's rules: whitespace separates
// tokens, '#' or ';' at the start of a token begins a comment, double quotes
// group with backslash escapes, single quotes group literally, and a
// backslash outside quotes escapes the next character.
func tokenize(line string) ([]string, error) {
	var (
		tokens  []string
		cur     strings.Builder
		inToken bool
		quote   rune
		escaped bool
	)
	flush := func() {
		if inToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			inToken = false
		}
	}
	for _, c := range line {
		switch {
		case escaped:
			cur.WriteRune(c)
			escaped = false
		case quote == '"':
			switch c {
			case '\\':
				escaped = true
			case '"':
				quote = 0
			default:
				cur.WriteRune(c)
			}
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\\':
			escaped = true
			inToken = true
		case c == '"' || c == '\'':
			quote = c
			inToken = true
		case unicode.IsSpace(c):
			flush()
		case (c == '#' || c == ';') && !inToken:
			flush()
			return tokens, nil
		default:
			cur.WriteRune(c)
			inToken = true
		}
	}
	if quote != 0 {
		flush()
		return tokens, fmt.Errorf("unterminated %c quote", quote)
	}
	flush()
	return tokens, nil
}

// Has reports whether a directive is present.
func (c *Config) Has(name string) bool {
	for _, d := range c.Directives {
		if d.Name == name {
			return true
		}
	}
	return false
}

// All returns every directive with the given name, in file order.
func (c *Config) All(name string) []Directive {
	var out []Directive
	for _, d := range c.Directives {
		if d.Name == name {
			out = append(out, d)
		}
	}
	return out
}

// Last returns the last occurrence of a directive (OpenVPN semantics for
// single-valued options: later wins).
func (c *Config) Last(name string) (Directive, bool) {
	for i := len(c.Directives) - 1; i >= 0; i-- {
		if c.Directives[i].Name == name {
			return c.Directives[i], true
		}
	}
	return Directive{}, false
}

// Inline returns the inline block with the given tag, if present.
func (c *Config) Inline(name string) (Directive, bool) {
	for i := len(c.Directives) - 1; i >= 0; i-- {
		d := c.Directives[i]
		if d.Inline && d.Name == name {
			return d, true
		}
	}
	return Directive{}, false
}

// Append returns a new Config with extra directives appended, used to model
// command-line overrides placed after --config.
func (c *Config) Append(ds ...Directive) *Config {
	out := &Config{
		Directives: append(append([]Directive(nil), c.Directives...), ds...),
		Errors:     c.Errors,
	}
	return out
}
