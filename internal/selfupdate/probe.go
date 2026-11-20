package selfupdate

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	releaseLatestURL = "https://github.com/" + repository + "/releases/latest"
	releaseTagPath   = "/releases/tag/"
	probeTimeout     = 8 * time.Second
)

// probeLatestTag reads the newest tag from GitHub's /releases/latest redirect
// without following it. One small, header-only request replaces both the API
// call and the checksums.txt download in background checks. An empty result
// means "could not decide"; callers must fall back to the full updater check.
func probeLatestTag(ctx context.Context, endpoint string) string {
	client := &http.Client{
		Timeout: probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return ""
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return ""
	}
	u, err := url.Parse(loc)
	if err != nil {
		return ""
	}
	idx := strings.Index(u.Path, releaseTagPath)
	if idx < 0 {
		return ""
	}
	tag := u.Path[idx+len(releaseTagPath):]
	if slash := strings.IndexByte(tag, '/'); slash >= 0 {
		tag = tag[:slash]
	}
	return tag
}

// probeSaysUpToDate returns true only when both versions are valid semver and
// the published tag is not newer. Invalid versions (e.g. "dev") deliberately
// return false so the full updater check still runs.
func probeSaysUpToDate(tag, current string) bool {
	t, c := canonicalSemver(tag), canonicalSemver(current)
	if !semver.IsValid(t) || !semver.IsValid(c) {
		return false
	}
	return semver.Compare(t, c) <= 0
}

func canonicalSemver(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	switch v[0] {
	case 'v':
	case 'V':
		v = "v" + v[1:]
	default:
		v = "v" + v
	}
	return v
}
