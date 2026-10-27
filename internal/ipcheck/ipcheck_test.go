package ipcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestParseBody(t *testing.T) {
	ok := map[string]string{
		"203.0.113.7\n":                       "203.0.113.7",
		"  2001:db8::1  ":                     "2001:db8::1",
		`{"ip":"198.51.100.2","country":"X"}`: "198.51.100.2",
		`{"query":"192.0.2.9"}`:               "192.0.2.9",
		"::ffff:192.0.2.1":                    "192.0.2.1",
	}
	for body, want := range ok {
		got, err := ParseBody([]byte(body))
		if err != nil || got.String() != want {
			t.Errorf("ParseBody(%q) = %v %v, want %s", body, got, err, want)
		}
	}
	for _, body := range []string{"", "<html><body>Your IP</body></html>", `{"status":"fail"}`, "not an ip"} {
		if _, err := ParseBody([]byte(body)); err == nil {
			t.Errorf("ParseBody(%q) should fail", body)
		}
	}
}

func TestCheckClassifiesErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		_, _ = w.Write([]byte("203.0.113.7\n"))
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ip":"203.0.113.7"}`)) })
	mux.HandleFunc("/down", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
	mux.HandleFunc("/404", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/html", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>hi</html>")) })
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewHTTPChecker(500*time.Millisecond, "test")
	services := []IPService{
		{Name: "ok", URL: srv.URL + "/ok"}, {Name: "json", URL: srv.URL + "/json"},
		{Name: "down", URL: srv.URL + "/down"}, {Name: "404", URL: srv.URL + "/404"},
		{Name: "html", URL: srv.URL + "/html"}, {Name: "slow", URL: srv.URL + "/slow"},
		{Name: "dns", URL: "http://does-not-exist.invalid/"},
	}
	res := c.Check(context.Background(), services)
	want := []ErrorKind{KindNone, KindNone, KindUnavailable, KindHTTP, KindInvalid, KindTimeout, KindDNS}
	for i, r := range res {
		if r.Service != services[i].Name {
			t.Fatalf("order not preserved")
		}
		if r.Kind != want[i] {
			// DNS resolution may be unavailable in sandboxes; accept a network error.
			if services[i].Name == "dns" && (r.Kind == KindNetwork || r.Kind == KindTimeout) {
				continue
			}
			t.Errorf("%s: kind %q (%v), want %q", r.Service, r.Kind, r.Err, want[i])
		}
	}
	sum := Summarize(res)
	if sum.OK != 2 || sum.Failed != 5 || !sum.Consistent() {
		t.Fatalf("summary %+v", sum)
	}
	if ip, ok := sum.Primary(); !ok || ip != netip.MustParseAddr("203.0.113.7") || !sum.Contains(ip) {
		t.Fatal("primary")
	}
}

func TestSummaryDisagreement(t *testing.T) {
	res := []Result{
		{IP: netip.MustParseAddr("192.0.2.1")}, {IP: netip.MustParseAddr("192.0.2.2")},
		{IP: netip.MustParseAddr("2001:db8::1")},
	}
	s := Summarize(res)
	if s.Consistent() || len(s.IPv4) != 2 || len(s.IPv6) != 1 {
		t.Fatalf("%+v", s)
	}
	// IPv4 and IPv6 differing is normal and consistent.
	s = Summarize(res[1:])
	if !s.Consistent() {
		t.Fatal("v4+v6 should be consistent")
	}
}
