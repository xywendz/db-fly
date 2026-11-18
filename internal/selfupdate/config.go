// Package selfupdate wires the Wails v3 built-in updater to the project's
// GitHub Releases. It only configures the updater; Wails owns download, digest
// verification, binary swap and restart.
package selfupdate

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

const (
	// CheckEvent is emitted by the frontend to trigger a manual update check.
	CheckEvent = "db-fly:check-updates"

	// Keep in sync with the git remote (`git remote get-url origin`). The
	// GitHub provider treats a 404 from /releases/latest as "up to date", so a
	// wrong owner silently disables every update check.
	repository    = "xywendz/db-fly"
	checksumAsset = "checksums.txt"

	checkInterval = 6 * time.Hour
	checkTimeout  = 10 * time.Minute
	helperTimeout = 45 * time.Second
	initialDelay  = 10 * time.Second
	httpTimeout   = 5 * time.Minute
	expectedOS    = "windows"
	expectedArch  = "amd64"
	assetNameBase = "db-fly-gui"
)

// gitDescribe matches `git describe --tags` output such as v1.2.3-4-gdeadbee.
// SemVer treats "-4-gdeadbee" as a pre-release, which would make the release
// tag v1.2.3 look newer than a build four commits after it. Move the distance
// into build metadata so such builds compare equal to their base tag.
var gitDescribe = regexp.MustCompile(`^([vV]?\d+\.\d+\.\d+)-(\d+)-(g[0-9a-fA-F]+)$`)

var checkMu sync.Mutex

// Init configures app.Updater once. An error means automatic updates are
// unavailable for this session; the application itself still starts normally.
func Init(u *updater.Updater, version string) error {
	if u == nil {
		return fmt.Errorf("selfupdate: updater is nil")
	}
	provider, err := github.New(github.Config{
		Repository:    repository,
		ChecksumAsset: checksumAsset,
		AssetMatcher:  matchAsset,
		HTTPClient:    &http.Client{Timeout: httpTimeout},
	})
	if err != nil {
		return fmt.Errorf("selfupdate: configure GitHub provider: %w", err)
	}
	return u.Init(updater.Config{
		CurrentVersion:     normalizeVersion(version),
		Providers:          []updater.Provider{provider},
		HelperReadyTimeout: helperTimeout,
	})
}

// StartBackgroundChecks checks shortly after startup and then every
// checkInterval. Background checks stay silent unless an update is actually
// available, so "already up to date" never opens a window.
func StartBackgroundChecks(ctx context.Context, u *updater.Updater) {
	go func() {
		timer := time.NewTimer(initialDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
			if err := check(ctx, u, false); err != nil {
				log.Printf("自动检查更新失败: %v", err)
			}
		case <-ctx.Done():
			return
		}

		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := check(ctx, u, false); err != nil {
					log.Printf("自动检查更新失败: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// CheckNow runs a user-visible check and always shows the updater window,
// including the "already up to date" result. Concurrent requests collapse
// into one flow.
func CheckNow(ctx context.Context, u *updater.Updater) error {
	return check(ctx, u, true)
}

func check(ctx context.Context, u *updater.Updater, alwaysShow bool) error {
	if u == nil {
		return fmt.Errorf("selfupdate: updater is nil")
	}
	if !checkMu.TryLock() {
		return nil
	}
	defer checkMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	if !alwaysShow {
		release, err := u.Check(checkCtx)
		if err != nil {
			return fmt.Errorf("selfupdate: check failed: %w", err)
		}
		if release == nil {
			return nil
		}
	}
	if err := u.CheckAndInstall(checkCtx); err != nil {
		return fmt.Errorf("selfupdate: show update failed: %w", err)
	}
	return nil
}

func normalizeVersion(version string) string {
	v := strings.TrimSpace(version)
	if m := gitDescribe.FindStringSubmatch(v); m != nil {
		return m[1] + "+" + m[2] + "." + m[3]
	}
	return v
}

// matchAsset selects the single-file Windows artifact published by the release
// workflow. A plain .gz is intentionally ignored: Wails unpacks .zip and
// .tar.gz, not a gzip-compressed bare executable.
func matchAsset(req updater.CheckRequest, assets []github.ReleaseAsset) int {
	if !strings.EqualFold(req.Platform, expectedOS) {
		return -1
	}
	arch := strings.ToLower(req.Arch)
	if arch == "" {
		arch = expectedArch
	}
	if arch != expectedArch {
		return -1
	}
	candidates := []string{
		fmt.Sprintf("%s-%s-%s.zip", assetNameBase, req.Platform, arch),
		fmt.Sprintf("%s-%s-%s.exe", assetNameBase, req.Platform, arch),
		assetNameBase + ".zip",
		assetNameBase + ".exe",
	}
	for _, name := range candidates {
		for i, asset := range assets {
			if strings.EqualFold(asset.Name, name) {
				return i
			}
		}
	}
	return -1
}
