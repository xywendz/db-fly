package selfupdate

import (
	"context"
	"io"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

// fastProvider wraps the GitHub provider with a header-only 302 probe on
// /releases/latest. When the probe proves the app is already up to date,
// Check returns (nil, nil) without touching the API or downloading the
// checksum sidecar. Both background and manual checks go through this path,
// so a TLS timeout on api.github.com no longer breaks "already up to date".
type fastProvider struct {
	inner     updater.Provider
	latestURL string
}

func newFastProvider(inner updater.Provider) updater.Provider {
	return &fastProvider{inner: inner, latestURL: releaseLatestURL}
}

func (p *fastProvider) Name() string { return p.inner.Name() }

func (p *fastProvider) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	if tag := probeLatestTag(ctx, p.latestURL); tag != "" && probeSaysUpToDate(tag, req.CurrentVersion) {
		return nil, nil
	}
	return p.inner.Check(ctx, req)
}

func (p *fastProvider) Download(ctx context.Context, r *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	return p.inner.Download(ctx, r, dst, onProgress)
}
