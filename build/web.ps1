# Build the B/S server (net/http + embedded Vue frontend).
# Frontend template: npm run build:web (--mode web) -> frontend/dist-web
# Go entry:          ./cmd/db-fly-server, pure Go, CGO_ENABLED=0
# NOTE: keep this script ASCII-only. Windows PowerShell 5.1 decodes BOM-less
# UTF-8 .ps1 files as ANSI, which corrupts non-ASCII text and breaks parsing.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

# ---- 1. Version (injected into internal/bridge.Version) ----
$version = "dev"
try { $version = (git describe --tags --always 2>$null).Trim() }
catch { }
if (-not $version) { $version = "dev" }

# ---- 2. Build the Web frontend template ----
Write-Host "==> Building Web frontend"
# Clean the output directory here instead of letting Vite emptyOutDir do it:
# some Windows environments intercept Node's delete calls.
Remove-Item -Recurse -Force frontend/dist-web -ErrorAction SilentlyContinue
Push-Location frontend
if (-not (Test-Path node_modules)) {
    npm install
    if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
}
npm run build:web
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

# ---- 3. Copy the output into the Go embed directory ----
$embed = "internal/webui/dist"
Remove-Item -Recurse -Force $embed -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $embed | Out-Null
Copy-Item -Recurse -Force frontend/dist-web/* $embed/
Write-Host "==> Frontend assets copied to $embed"

# ---- 4. Go build (Linux amd64 by default; override with GOOS_TARGET / GOARCH_TARGET) ----
$env:CGO_ENABLED = "0"
$targetOS = if ($env:GOOS_TARGET) { $env:GOOS_TARGET } else { "linux" }
$targetArch = if ($env:GOARCH_TARGET) { $env:GOARCH_TARGET } else { "amd64" }
$env:GOOS = $targetOS
$env:GOARCH = $targetArch

New-Item -ItemType Directory -Force -Path dist | Out-Null
$output = "dist/db-fly-server"
if ($targetOS -eq "windows") { $output += ".exe" }
go build -trimpath `
  -ldflags "-s -w -X github.com/db-fly/db-fly/internal/bridge.Version=$version" `
  -o $output ./cmd/db-fly-server
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "Built $output (GOOS=$targetOS GOARCH=$targetArch, version $version)"
Write-Host "Run: DB_FLY_WEB_ADDR=127.0.0.1:8080 $output   (open /login to register)"
