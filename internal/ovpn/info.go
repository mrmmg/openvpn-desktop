package ovpn

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Remote is a server endpoint from a `remote` directive.
type Remote struct {
	Host  string
	Port  int
	Proto string
}

// FileRef is a file referenced by the configuration.
type FileRef struct {
	Directive string
	// Path is as written in the config ("" when inline).
	Path string
	// Resolved is Path made absolute against the profile directory.
	Resolved string
	Inline   bool
	Optional bool
	Line     int
}

// AuthUserPass describes the auth-user-pass requirement.
type AuthUserPass struct {
	Required bool
	// File is the credential file named in the config (may be relative).
	File   string
	Inline bool
	Line   int
}

// Script is an external command the configuration asks OpenVPN to run.
type Script struct {
	Directive string
	Command   string
	Line      int
}

// fileDirectives lists options whose first argument is a file path.
var fileDirectives = map[string]bool{
	"ca": true, "cert": true, "key": true, "pkcs12": true, "dh": true,
	"tls-auth": true, "tls-crypt": true, "tls-crypt-v2": true, "secret": true,
	"crl-verify": true, "extra-certs": true, "http-proxy-user-pass": true,
}

// scriptDirectives run commands, typically as root.
var scriptDirectives = map[string]bool{
	"up": true, "down": true, "route-up": true, "route-pre-down": true,
	"ipchange": true, "tls-verify": true, "learn-address": true,
	"client-connect": true, "client-disconnect": true, "auth-user-pass-verify": true,
	"dns-updown": true,
}

// IsClient reports whether the configuration is a client configuration.
func (c *Config) IsClient() bool {
	return c.Has("client") || (c.Has("tls-client") && c.Has("pull"))
}

// Pulls reports whether the client accepts pushed options from the server.
func (c *Config) Pulls() bool { return c.Has("client") || c.Has("pull") }

// Remotes returns the configured servers, applying default port/proto.
// <connection> blocks inherit the global port and protocol.
func (c *Config) Remotes() []Remote {
	return c.remotes(1194, "udp")
}

func (c *Config) remotes(defPort int, defProto string) []Remote {
	port := defPort
	if d, ok := c.Last("port"); ok {
		if p, err := strconv.Atoi(d.Arg(0)); err == nil {
			port = p
		}
	}
	if d, ok := c.Last("rport"); ok {
		if p, err := strconv.Atoi(d.Arg(0)); err == nil {
			port = p
		}
	}
	proto := defProto
	if d, ok := c.Last("proto"); ok && d.Arg(0) != "" {
		proto = d.Arg(0)
	}
	var out []Remote
	for _, d := range c.All("remote") {
		r := Remote{Host: d.Arg(0), Port: port, Proto: proto}
		if p, err := strconv.Atoi(d.Arg(1)); err == nil {
			r.Port = p
		}
		if d.Arg(2) != "" {
			r.Proto = d.Arg(2)
		}
		out = append(out, r)
	}
	for _, d := range c.Directives {
		if d.Inline && d.Name == "connection" {
			sub, err := Parse(strings.NewReader(d.Content))
			if err == nil {
				out = append(out, sub.remotes(port, proto)...)
			}
		}
	}
	return out
}

// Device returns the device name and type (tun/tap/null).
func (c *Config) Device() (name, typ string) {
	if d, ok := c.Last("dev"); ok {
		name = d.Arg(0)
	}
	if d, ok := c.Last("dev-type"); ok {
		typ = d.Arg(0)
	}
	if typ == "" {
		switch {
		case strings.HasPrefix(name, "tap"):
			typ = "tap"
		case strings.HasPrefix(name, "null"):
			typ = "null"
		default:
			typ = "tun"
		}
	}
	return name, typ
}

// Auth returns the auth-user-pass requirement.
func (c *Config) Auth() AuthUserPass {
	var a AuthUserPass
	if d, ok := c.Last("auth-user-pass"); ok {
		a.Required = true
		a.Line = d.Line
		if d.Inline {
			a.Inline = true
		} else if arg := d.Arg(0); arg != "" {
			if arg == "[inline]" {
				a.Inline = true
			} else {
				a.File = arg
			}
		}
	}
	return a
}

// BaseDir returns the directory relative paths resolve against: the `cd`
// directive if present, else profileDir.
func (c *Config) BaseDir(profileDir string) string {
	if d, ok := c.Last("cd"); ok && d.Arg(0) != "" {
		if filepath.IsAbs(d.Arg(0)) {
			return d.Arg(0)
		}
		return filepath.Join(profileDir, d.Arg(0))
	}
	return profileDir
}

// FileRefs returns every file referenced by the configuration.
func (c *Config) FileRefs(profileDir string) []FileRef {
	base := c.BaseDir(profileDir)
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	var out []FileRef
	for _, d := range c.Directives {
		switch {
		case d.Inline && (fileDirectives[d.Name] || d.Name == "auth-user-pass"):
			out = append(out, FileRef{Directive: d.Name, Inline: true, Line: d.Line})
		case fileDirectives[d.Name]:
			p := d.Arg(0)
			if p == "" {
				continue
			}
			if p == "[inline]" {
				out = append(out, FileRef{Directive: d.Name, Inline: true, Line: d.Line})
				continue
			}
			out = append(out, FileRef{Directive: d.Name, Path: p, Resolved: resolve(p), Line: d.Line})
		case d.Name == "auth-user-pass" || d.Name == "askpass":
			if p := d.Arg(0); p != "" && p != "[inline]" {
				out = append(out, FileRef{Directive: d.Name, Path: p, Resolved: resolve(p), Line: d.Line})
			}
		case d.Name == "http-proxy":
			// http-proxy server port [authfile|auto|auto-nct] [method]
			if p := d.Arg(2); p != "" && p != "auto" && p != "auto-nct" && p != "stdin" {
				out = append(out, FileRef{Directive: d.Name, Path: p, Resolved: resolve(p), Line: d.Line})
			}
		}
	}
	return out
}

// Scripts returns the external commands configured to run.
func (c *Config) Scripts() []Script {
	var out []Script
	for _, d := range c.Directives {
		if scriptDirectives[d.Name] && !d.Inline && len(d.Args) > 0 {
			out = append(out, Script{Directive: d.Name, Command: strings.Join(d.Args, " "), Line: d.Line})
		}
	}
	return out
}

// ScriptSecurity returns the configured script-security level (default 1).
func (c *Config) ScriptSecurity() int {
	if d, ok := c.Last("script-security"); ok {
		if n, err := strconv.Atoi(d.Arg(0)); err == nil {
			return n
		}
	}
	return 1
}
