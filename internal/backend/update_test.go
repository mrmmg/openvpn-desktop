package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.1", "0.1.0", 1},
		{"0.2.0", "0.10.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"v1.0.0", "1.0.0", 0},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	const body = `[
	 {"tag_name":"v0.3.0-rc.1","html_url":"u/rc","prerelease":true},
	 {"tag_name":"v0.2.0","html_url":"u/0.2.0"},
	 {"tag_name":"v0.9.0","html_url":"u/draft","draft":true},
	 {"tag_name":"v0.1.0","html_url":"u/0.1.0"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	run := func(cur string) UpdateInfo { return checkUpdate(context.Background(), srv.Client(), srv.URL, cur) }

	if got := run("0.1.0"); !got.Available || got.Latest != "0.2.0" || got.URL != "u/0.2.0" || got.Command == "" {
		t.Errorf("stable build: %+v", got)
	}
	if got := run("0.2.0"); got.Available || got.Error != "" {
		t.Errorf("already latest: %+v", got)
	}
	if got := run("0.3.0-rc.1"); got.Available {
		t.Errorf("already on the newest pre-release: %+v", got)
	}
	if got := run("0.3.0-rc.0"); !got.Available || got.Latest != "0.3.0-rc.1" {
		t.Errorf("pre-release build: %+v", got)
	}
	if got := run("dev"); got.Error == "" || got.Available {
		t.Errorf("dev build: %+v", got)
	}
}
