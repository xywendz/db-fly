# Build the Windows GUI (wails3 + Vue).
# wails3 v3 on Windows is pure Go: CGO_ENABLED=0, no mingw-w64 gcc needed.
# The real entry is main.go (//go:build windows); stub.go is only for non-Windows.
# NOTE: keep this script ASCII-only. Windows PowerShell 5.1 decodes BOM-less
# UTF-8 .ps1 files as ANSI, which corrupts non-ASCII text and breaks parsing.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

# ---- 1. Version (injected into internal/bridge.Version) ----
$version = "dev"
try { $version = (git describe --tags --always 2>$null).Trim() }
catch { }
if (-not $version) { $version = "dev" }

# ---- 2. Build the Vue frontend ----
Write-Host "==> Building Vue frontend"
Push-Location frontend
if (-not (Test-Path node_modules)) {
    npm install
}
npm run build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Pop-Location

# ---- 3. Copy the frontend output into the Go embed directory ----
$assets = "cmd/db-fly-gui/assets"
Remove-Item -Recurse -Force $assets -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $assets | Out-Null
Copy-Item -Recurse -Force frontend/dist/* $assets/
Write-Host "==> Frontend assets copied to $assets"

# ---- 4. Application icon, derived from the repository-root logo.png ----
# logo.png is the single source of truth. This step refreshes:
#   cmd/db-fly-gui/icon.png             -> //go:embed, Options.Icon (Linux/macOS)
#   build/windows/icon.ico             -> multi-size Windows icon
#   cmd/db-fly-gui/rsrc_windows_*.syso -> linked into the .exe (Explorer/taskbar icon)
$wails = Get-Command wails3 -ErrorAction SilentlyContinue
if (-not $wails) {
    throw "wails3 CLI not found. Install it with: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0"
}

$guiDir = "cmd/db-fly-gui"
$iconDir = "build/windows"
$iconFile = Join-Path $iconDir "icon.ico"
$arch = (go env GOARCH).Trim()
$syso = Join-Path $guiDir "rsrc_windows_$arch.syso"

Copy-Item -Force logo.png (Join-Path $guiDir "icon.png")
New-Item -ItemType Directory -Force -Path $iconDir | Out-Null

# The CLI also writes a Mac icon; redirect that unused file to TEMP on Windows.
$macTmp = Join-Path $env:TEMP "db-fly-icon.icns"
wails3 generate icons -input logo.png -windowsfilename $iconFile -macfilename $macTmp
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# generate syso is pure Go (winres): no windres / gcc needed for this step.
# It embeds the .ico, the app manifest and version info as PE resources.
wails3 generate syso -icon $iconFile `
    -manifest (Join-Path $iconDir "wails.exe.manifest") `
    -info (Join-Path $iconDir "info.json") `
    -arch $arch -out $syso
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "==> Windows icon resource: $syso"

# ---- 5. Go build (pure Go; wails3 v3 needs no CGO on Windows) ----
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"

New-Item -ItemType Directory -Force -Path dist | Out-Null
go build -trimpath `
  -ldflags "-s -w -H windowsgui -X github.com/db-fly/db-fly/internal/bridge.Version=$version" `
  -o dist/db-fly-gui.exe ./cmd/db-fly-gui
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# ---- 6. Guard against producing a silent stub ----
# The real entry imports wails3; the non-Windows stub does not. `go version -m`
# therefore proves the GUI (not the stub) was linked into the binary.
$bin = (Resolve-Path .\dist\db-fly-gui.exe).Path
$buildInfo = (go version -m $bin) -join "`n"
if ($buildInfo -notmatch "github.com/wailsapp/wails/v3") {
    Remove-Item -Force $bin
    throw "The binary does not contain the wails3 GUI entry (stub built by mistake). Check the build tags in cmd/db-fly-gui."
}

Write-Host "Built dist/db-fly-gui.exe (pure Go / CGO_ENABLED=0, version $version, icon from logo.png)"
