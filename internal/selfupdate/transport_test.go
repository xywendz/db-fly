package selfupdate

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestParseReleaseDownloadURL(t *testing.T) {
	u, _ := url.Parse("https://github.com/xywendz/db-fly/releases/download/v1.0.5/checksums.txt")
	tag, name, ok := parseReleaseDownloadURL(u, repository)
	if !ok || tag != "v1.0.5" || name != "checksums.txt" {
		t.Fatalf("parse = %q, %q, %v", tag, name, ok)
	}

	u, _ = url.Parse("https://api.github.com/repos/xywendz/db-fly/releases/latest")
	if _, _, ok := parseReleaseDownloadURL(u, repository); ok {
		t.Fatal("api.github.com URL must not be rewritten")
	}
}

func TestGitHubAssetTransportUsesAssetAPI(t *testing.T) {
	var assetAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/tags/v1.0.5"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"assets":[{"id":766914352,"name":"checksums.txt"}]}`)
		case strings.HasSuffix(r.URL.Path, "/releases/assets/766914352"):
			assetAccept = r.Header.Get("Accept")
			fmt.Fprint(w, "abc  checksums.txt\n")
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "github.com" {
			return nil, fmt.Errorf("direct github.com must not be used")
		}
		return http.DefaultTransport.RoundTrip(req)
	})
	tr := newGitHubAssetTransport(base, srv.URL)
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	resp, err := client.Get("https://github.com/xywendz/db-fly/releases/download/v1.0.5/checksums.txt")
	if err != nil {
		t.Fatalf("get checksum: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "abc  checksums.txt\n" {
		t.Fatalf("body = %q", body)
	}
	if assetAccept != "application/octet-stream" {
		t.Fatalf("Accept = %q", assetAccept)
	}

	// Second request should hit the cached asset id without another lookup.
	if _, err := tr.assetID(t.Context(), "v1.0.5", "checksums.txt"); err != nil {
		t.Fatalf("cached asset id: %v", err)
	}
}
