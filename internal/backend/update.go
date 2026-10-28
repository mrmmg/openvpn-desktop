package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	releasesAPI = "https://api.github.com/repos/mrmmg/openvpn-desktop/releases?per_page=20"
	installURL  = "https://raw.githubusercontent.com/mrmmg/openvpn-desktop/main/install.sh"
)

// UpdateInfo is the result of a check against the GitHub releases.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	// URL is the release page; Command updates the app from a terminal.
	URL     string `json:"url"`
	Command string `json:"command"`
	// Error is set when the check could not be completed.
	Error string `json:"error"`
}

type githubRelease struct {
	Tag        string `json:"tag_name"`
	URL        string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// checkUpdate asks the releases API for a version newer than current. Stable
// builds only see stable releases; a pre-release build also sees pre-releases.
func checkUpdate(ctx context.Context, client *http.Client, api, current string) UpdateInfo {
	info := UpdateInfo{Current: current}
	if _, ok := parseVersion(current); !ok {
		info.Error = "this is a development build"
		return info
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "openvpn-desktop/"+current)
	resp, err := client.Do(req)
	if err != nil {
		info.Error = "could not reach GitHub"
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		info.Error = fmt.Sprintf("GitHub answered %s", resp.Status)
		return info
	}
	var list []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		info.Error = "unexpected answer from GitHub"
		return info
	}
	curPre := isPrerelease(current)
	best := current
	var bestRel *githubRelease
	for i, r := range list {
		v := strings.TrimPrefix(r.Tag, "v")
		if r.Draft {
			continue
		}
		if _, ok := parseVersion(v); !ok || (r.Prerelease && !curPre) {
			continue
		}
		if compareVersions(v, best) > 0 {
			best, bestRel = v, &list[i]
		}
	}
	info.Latest = best
	if bestRel != nil {
		info.Available, info.URL = true, bestRel.URL
		info.Command = fmt.Sprintf("curl -fsSL %s | sh -s -- v%s", installURL, best)
	}
	return info
}

type semver struct {
	core [3]int
	pre  []string
}

func parseVersion(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		v.core[i] = n
	}
	if pre != "" {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

func isPrerelease(s string) bool {
	v, ok := parseVersion(s)
	return ok && len(v.pre) > 0
}

// compareVersions orders two semantic versions (-1, 0, 1); unparsable input
// compares as equal.
func compareVersions(a, b string) int {
	x, okA := parseVersion(a)
	y, okB := parseVersion(b)
	if !okA || !okB {
		return 0
	}
	for i := range x.core {
		if x.core[i] != y.core[i] {
			return cmpInt(x.core[i], y.core[i])
		}
	}
	// A release outranks its own pre-releases.
	if len(x.pre) == 0 || len(y.pre) == 0 {
		return cmpInt(len(y.pre), len(x.pre))
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		p, q := x.pre[i], y.pre[i]
		if p == q {
			continue
		}
		pn, errP := strconv.Atoi(p)
		qn, errQ := strconv.Atoi(q)
		switch {
		case errP == nil && errQ == nil:
			return cmpInt(pn, qn)
		case errP == nil: // numeric identifiers rank below alphanumeric ones
			return -1
		case errQ == nil:
			return 1
		}
		return strings.Compare(p, q)
	}
	return cmpInt(len(x.pre), len(y.pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func updateClient() *http.Client { return &http.Client{Timeout: 8 * time.Second} }
