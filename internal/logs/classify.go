// Package logs reads, follows and interprets OpenVPN log files.
package logs

import (
	"regexp"
	"strings"
)

// Class categorises a log line for highlighting and filtering.
type Class int

// Line classes, roughly by importance.
const (
	Plain Class = iota
	Debug
	Connection
	Route
	Warning
	TLS
	Auth
	Error
)

type rule struct {
	class Class
	re    *regexp.Regexp
}

// Order matters: the first matching rule wins.
var rules = []rule{
	{Auth, regexp.MustCompile(`AUTH_FAILED|auth-failure|Verification Failed: 'Auth'|ERROR: could not read Auth|AUTH: Received control message: AUTH_FAILED`)},
	{TLS, regexp.MustCompile(`TLS Error|TLS handshake failed|VERIFY ERROR|certificate verify failed|tls-crypt unwrapping failed|TLS key negotiation failed|SSL3_|OpenSSL: error`)},
	{Error, regexp.MustCompile(`(?i)\bfatal\b|Options error|ERROR:|Cannot (open|resolve|ioctl|allocate)|Exiting due to|Linux route add command failed|RESOLVE: Cannot resolve|Network is unreachable|Connection refused|failed, will try again|No such file`)},
	{Warning, regexp.MustCompile(`(?i)\bWARNING\b|DEPRECATED|Inactivity timeout|ping-restart|SIGUSR1|restarting|Connection reset`)},
	{Route, regexp.MustCompile(`net_route_v[46]_(add|del)|/sbin/ip route|route-gateway|redirect-gateway|PUSH: Received control message|Pushed option removed|add_route|delete_route|ROUTE_GATEWAY|ROUTE6_GATEWAY`)},
	{Connection, regexp.MustCompile(`Initialization Sequence Completed|Peer Connection Initiated|TCP connection established|UDPv[46] link|TUN/TAP device .* opened|DCO device .* opened|net_addr_v[46]_add|SIGTERM|process exiting|Attempting to establish|MANAGEMENT: Client`)},
	{Debug, regexp.MustCompile(`MANAGEMENT: CMD|^\s*$`)},
}

// Classify returns the class of a log line.
func Classify(line string) Class {
	for _, r := range rules {
		if r.re.MatchString(line) {
			return r.class
		}
	}
	return Plain
}

var tsRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?|\w{3} \w{3} +\d+ \d{2}:\d{2}:\d{2} \d{4}) `)

// SplitTimestamp separates OpenVPN's timestamp prefix from the message.
func SplitTimestamp(line string) (ts, msg string) {
	if m := tsRe.FindStringSubmatchIndex(line); m != nil {
		return line[m[2]:m[3]], line[m[1]:]
	}
	return "", line
}

// Failure is a human explanation of why a connection ended.
type Failure struct {
	Summary string
	Hint    string
	Line    string
	// Auth is true for authentication failures (no auto-reconnect).
	Auth bool
}

type failureRule struct {
	re      *regexp.Regexp
	summary string
	hint    string
	auth    bool
}

var failureRules = []failureRule{
	{regexp.MustCompile(`AUTH_FAILED|Verification Failed: 'Auth'`), "Authentication failed", "Check the username and password (credential file).", true},
	{regexp.MustCompile(`could not read Auth username`), "No credentials supplied", "Create a credential file or enter them when asked.", true},
	{regexp.MustCompile(`Options error: (.*)`), "Invalid configuration", "Run validation on the profile for details.", false},
	{regexp.MustCompile(`Cannot ioctl TUNSETIFF|Cannot open TUN/TAP dev|Operation not permitted`), "Cannot create the tunnel device", "OpenVPN needs root (CAP_NET_ADMIN); check privilege settings.", false},
	{regexp.MustCompile(`RESOLVE: Cannot resolve host address|Cannot resolve host address`), "Cannot resolve the VPN server's name", "Check DNS and the 'remote' host name.", false},
	{regexp.MustCompile(`TLS key negotiation failed|TLS handshake failed`), "TLS handshake failed", "Server unreachable, wrong port/protocol, firewall, or tls-auth/tls-crypt key mismatch.", false},
	{regexp.MustCompile(`VERIFY ERROR|certificate verify failed`), "Certificate verification failed", "The server certificate does not match the CA or verify-x509-name.", false},
	{regexp.MustCompile(`Connection refused`), "Connection refused by the server", "Wrong port or server down.", false},
	{regexp.MustCompile(`Network is unreachable`), "Network is unreachable", "No route to the VPN server; check your Internet connection.", false},
	{regexp.MustCompile(`Inactivity timeout`), "Connection timed out", "The server stopped responding (ping-restart).", false},
	{regexp.MustCompile(`Exiting due to fatal error`), "OpenVPN exited due to a fatal error", "See the log for the preceding error.", false},
	{regexp.MustCompile(`SIGTERM\[(?:hard|soft),[^\]]*\] received, process exiting`), "Stopped (SIGTERM)", "", false},
}

// FindFailure scans lines from the end and returns the most specific
// explanation of a failure, or nil when none is recognised.
func FindFailure(lines []string) *Failure {
	// Look at the last 200 lines only; earlier sessions are irrelevant.
	start := max(0, len(lines)-200)
	var generic *Failure
	for i := len(lines) - 1; i >= start; i-- {
		line := lines[i]
		for idx, r := range failureRules {
			m := r.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			_, msg := SplitTimestamp(line)
			f := &Failure{Summary: r.summary, Hint: r.hint, Line: strings.TrimSpace(msg), Auth: r.auth}
			if len(m) > 1 && m[1] != "" {
				f.Summary += ": " + m[1]
			}
			// "Exiting due to fatal error" and SIGTERM are generic: keep
			// looking for the specific cause above them.
			if idx >= len(failureRules)-2 {
				if generic == nil {
					generic = f
				}
				continue
			}
			return f
		}
		if strings.Contains(line, "Initialization Sequence Completed") && generic != nil {
			break
		}
	}
	return generic
}
