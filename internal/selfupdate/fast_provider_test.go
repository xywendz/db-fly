package selfupdate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

type countingProvider struct {
	calls int
	rel   *updater.Release
	err   error
}

func (p *countingProvider) Name() string { return "counting" }
func (p *countingProvider) Check(context.Context, updater.CheckRequest) (*updater.Release, error) {
	p.calls++
	return p.rel, p.err
}
func (p *countingProvider) Download(context.Context, *updater.Release, io.Writer, func(int64, int64)) error {
	return nil
}

func TestFastProviderSkipsAPIWhenUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/xywendz/db-fly/releases/tag/v1.0.5")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	inner := &countingProvider{err: errors.New("API must not be called")}
	p := &fastProvider{inner: inner, latestURL: srv.URL}

	rel, err := p.Check(context.Background(), updater.CheckRequest{CurrentVersion: "v1.0.5"})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if rel != nil {
		t.Fatalf("Check returned release for up-to-date version: %+v", rel)
	}
	if inner.calls != 0 {
		t.Fatalf("inner provider called %d times, want 0", inner.calls)
	}
}

func TestFastProviderFallsBackWhenNewer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/xywendz/db-fly/releases/tag/v2.0.0")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	want := &updater.Release{Version: "2.0.0"}
	inner := &countingProvider{rel: want}
	p := &fastProvider{inner: inner, latestURL: srv.URL}

	rel, err := p.Check(context.Background(), updater.CheckRequest{CurrentVersion: "v1.0.5"})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if rel != want {
		t.Fatalf("Check returned %+v, want inner release", rel)
	}
	if inner.calls != 1 {
		t.Fatalf("inner provider called %d times, want 1", inner.calls)
	}
}
