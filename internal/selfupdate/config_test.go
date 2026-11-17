package selfupdate

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"v1.2.3":            "v1.2.3",
		"v1.2.3-4-gdeadbee": "v1.2.3+4.gdeadbee",
		"1.2.3-9-gabc1234":  "1.2.3+9.gabc1234",
		"dev":               "dev",
		"":                  "",
	}
	for in, want := range cases {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchAsset(t *testing.T) {
	assets := []github.ReleaseAsset{
		{Name: "checksums.txt"},
		{Name: "db-fly-gui.exe.gz"},
		{Name: "db-fly-gui-windows-amd64.zip"},
		{Name: "db-fly-gui.exe"},
	}
	req := updater.CheckRequest{Platform: "windows", Arch: "amd64"}
	if got := matchAsset(req, assets); got != 2 {
		t.Fatalf("matchAsset() = %d, want zip at index 2", got)
	}

	if got := matchAsset(updater.CheckRequest{Platform: "linux", Arch: "amd64"}, assets); got != -1 {
		t.Fatalf("linux match = %d, want -1", got)
	}
	if got := matchAsset(updater.CheckRequest{Platform: "windows", Arch: "arm64"}, assets); got != -1 {
		t.Fatalf("arm64 match = %d, want -1", got)
	}

	onlyGz := []github.ReleaseAsset{{Name: "db-fly-gui.exe.gz"}}
	if got := matchAsset(req, onlyGz); got != -1 {
		t.Fatalf("gzip-only match = %d, want -1", got)
	}
}
