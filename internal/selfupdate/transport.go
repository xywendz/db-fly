package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	githubAPIURL          = "https://api.github.com"
	directFallbackTimeout = 15 * time.Second
)

// githubAssetTransport keeps release downloads on GitHub while avoiding the
// github.com/.../releases/download host, which is unreachable from some
// networks even when api.github.com works. The provider hands us that URL in
// both the checksum sidecar and the artifact download; we look up the asset id
// through the API and fetch it from the API asset endpoint instead. The API
// redirects to the same release-assets.githubusercontent.com object, so the
// bytes and the checksum verification are unchanged.
type githubAssetTransport struct {
	base    http.RoundTripper
	apiBase string
	repo    string

	mu     sync.Mutex
	ids    map[string]int64
	lookup *http.Client
}

func newGitHubAssetTransport(base http.RoundTripper, apiBase string) *githubAssetTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	apiBase = strings.TrimRight(apiBase, "/")
	if apiBase == "" {
		apiBase = githubAPIURL
	}
	return &githubAssetTransport{
		base:    base,
		apiBase: apiBase,
		repo:    repository,
		ids:     make(map[string]int64),
		lookup:  &http.Client{Transport: base, Timeout: 15 * time.Second},
	}
}

func (t *githubAssetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tag, name, ok := parseReleaseDownloadURL(req.URL, t.repo)
	if !ok || req.Method != http.MethodGet {
		return t.base.RoundTrip(req)
	}

	id, err := t.assetID(req.Context(), tag, name)
	if err != nil {
		log.Printf("selfupdate: 无法通过 API 定位 %s/%s，回退直连: %v", tag, name, err)
		return t.direct(req)
	}

	out := req.Clone(req.Context())
	out.URL, err = url.Parse(fmt.Sprintf("%s/repos/%s/releases/assets/%d", t.apiBase, t.repo, id))
	if err != nil {
		return t.direct(req)
	}
	out.Host = out.URL.Host
	out.Header.Set("Accept", "application/octet-stream")

	resp, err := t.base.RoundTrip(out)
	if err != nil || resp.StatusCode >= 400 {
		if resp != nil {
			resp.Body.Close()
		}
		return t.direct(req)
	}
	return resp, nil
}

// direct uses a short deadline so a blocked github.com cannot stall the whole
// update check for the duration of the outer HTTP client timeout.
func (t *githubAssetTransport) direct(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), directFallbackTimeout)
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

func (t *githubAssetTransport) assetID(ctx context.Context, tag, name string) (int64, error) {
	key := tag + "\x00" + name
	t.mu.Lock()
	defer t.mu.Unlock()
	if id, ok := t.ids[key]; ok {
		return id, nil
	}

	endpoint := fmt.Sprintf("%s/repos/%s/releases/tags/%s", t.apiBase, t.repo, url.PathEscape(tag))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := t.lookup.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub API 查询 release %s 返回 HTTP %d", tag, resp.StatusCode)
	}

	var payload struct {
		Assets []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return 0, err
	}
	for _, asset := range payload.Assets {
		if asset.Name == name {
			t.ids[key] = asset.ID
			return asset.ID, nil
		}
	}
	return 0, fmt.Errorf("release %s 中没有资产 %s", tag, name)
}

func parseReleaseDownloadURL(u *url.URL, repo string) (tag, name string, ok bool) {
	if u == nil || !strings.EqualFold(u.Host, "github.com") {
		return "", "", false
	}
	prefix := "/" + repo + "/releases/download/"
	if !strings.HasPrefix(u.Path, prefix) {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, prefix), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
