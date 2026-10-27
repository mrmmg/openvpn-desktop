// Package ipcheck queries several public "what is my IP" services
// concurrently and classifies failures.
package ipcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrorKind classifies a failed lookup.
type ErrorKind string

// Error kinds.
const (
	KindNone        ErrorKind = ""
	KindTimeout     ErrorKind = "timeout"
	KindDNS         ErrorKind = "DNS failure"
	KindHTTP        ErrorKind = "HTTP error"
	KindUnavailable ErrorKind = "service unavailable"
	KindInvalid     ErrorKind = "invalid response"
	KindNetwork     ErrorKind = "network error"
)

// Result is the outcome for one service.
type Result struct {
	Service  string
	URL      string
	IP       netip.Addr
	Err      error
	Kind     ErrorKind
	Duration time.Duration
}

// OK reports success.
func (r Result) OK() bool { return r.Err == nil && r.IP.IsValid() }

// Checker performs lookups.
type Checker interface {
	Check(ctx context.Context, services []IPService) []Result
}

// HTTPChecker uses net/http.
type HTTPChecker struct {
	Client    *http.Client
	Timeout   time.Duration
	UserAgent string
}

// NewHTTPChecker returns a checker with sane transport settings. Keep-alives
// are disabled so every check opens a new connection through the *current*
// routing table (an old connection could still use the pre-VPN path).
func NewHTTPChecker(timeout time.Duration, version string) *HTTPChecker {
	tr := &http.Transport{
		Proxy:               nil, // a proxy would report the proxy's address
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: timeout,
		DialContext:         (&net.Dialer{Timeout: timeout}).DialContext,
		MaxIdleConns:        0,
		ForceAttemptHTTP2:   true,
	}
	return &HTTPChecker{
		Client:    &http.Client{Transport: tr, Timeout: timeout},
		Timeout:   timeout,
		UserAgent: "curl/8.0 (compatible; openvpn-wails/" + version + ")",
	}
}

// Check queries all services concurrently and returns results in the order
// of services.
func (c *HTTPChecker) Check(ctx context.Context, services []IPService) []Result {
	results := make([]Result, len(services))
	var wg sync.WaitGroup
	for i, s := range services {
		wg.Go(func() {
			results[i] = c.one(ctx, s)
		})
	}
	wg.Wait()
	return results
}

func (c *HTTPChecker) one(ctx context.Context, s IPService) Result {
	r := Result{Service: s.Name, URL: s.URL}
	start := time.Now()
	defer func() { r.Duration = time.Since(start) }()
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		r.Err, r.Kind = err, KindInvalid
		return r
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "text/plain, application/json;q=0.9, */*;q=0.1")
	resp, err := c.Client.Do(req)
	if err != nil {
		r.Err, r.Kind = err, classify(err)
		return r
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		r.Err, r.Kind = err, classify(err)
		return r
	}
	if resp.StatusCode >= 400 {
		r.Err = fmt.Errorf("HTTP %d", resp.StatusCode)
		r.Kind = KindHTTP
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			r.Kind = KindUnavailable
		}
		return r
	}
	ip, err := ParseBody(body)
	if err != nil {
		r.Err, r.Kind = err, KindInvalid
		return r
	}
	r.IP = ip
	return r
}

func classify(err error) ErrorKind {
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	case errors.As(err, &dnsErr):
		if dnsErr.IsTimeout {
			return KindTimeout
		}
		return KindDNS
	case errors.As(err, &netErr) && netErr.Timeout():
		return KindTimeout
	case strings.Contains(err.Error(), "connection refused"):
		return KindUnavailable
	}
	return KindNetwork
}

// ParseBody extracts an IP address from a plain-text or JSON response.
func ParseBody(body []byte) (netip.Addr, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return netip.Addr{}, errors.New("empty response")
	}
	if strings.HasPrefix(text, "{") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(text), &obj); err == nil {
			for _, k := range []string{"ip", "ip_addr", "address", "query", "origin"} {
				if v, ok := obj[k].(string); ok {
					if a, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
						return a.Unmap(), nil
					}
				}
			}
		}
		return netip.Addr{}, errors.New("JSON response without an IP field")
	}
	// Plain text: accept a single address (first line).
	first := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if a, err := netip.ParseAddr(first); err == nil {
		return a.Unmap(), nil
	}
	if strings.Contains(strings.ToLower(text), "<html") {
		return netip.Addr{}, errors.New("received an HTML page instead of an address (use a plain-text endpoint)")
	}
	if len(first) > 60 {
		first = first[:60] + "…"
	}
	return netip.Addr{}, fmt.Errorf("unexpected response %q", first)
}

// Summary aggregates results.
type Summary struct {
	IPv4, IPv6 []netip.Addr // distinct addresses seen per family
	OK, Failed int
}

// Summarize groups successful results by address family.
func Summarize(results []Result) Summary {
	var s Summary
	for _, r := range results {
		if !r.OK() {
			s.Failed++
			continue
		}
		s.OK++
		if r.IP.Is4() {
			if !slices.Contains(s.IPv4, r.IP) {
				s.IPv4 = append(s.IPv4, r.IP)
			}
		} else if !slices.Contains(s.IPv6, r.IP) {
			s.IPv6 = append(s.IPv6, r.IP)
		}
	}
	return s
}

// Consistent reports whether all services agree per family.
func (s Summary) Consistent() bool { return len(s.IPv4) <= 1 && len(s.IPv6) <= 1 }

// Primary returns the address to show as "the" public IP.
func (s Summary) Primary() (netip.Addr, bool) {
	if len(s.IPv4) > 0 {
		return s.IPv4[0], true
	}
	if len(s.IPv6) > 0 {
		return s.IPv6[0], true
	}
	return netip.Addr{}, false
}

// Contains reports whether addr was reported by any service.
func (s Summary) Contains(addr netip.Addr) bool {
	return slices.Contains(s.IPv4, addr) || slices.Contains(s.IPv6, addr)
}
