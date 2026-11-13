# Full verification for both modes: Go build / vet / tests (pure Go),
# Vue type-check and BOTH frontend templates, plus a Linux cross build of the
# B/S entry. This does NOT produce a GUI executable (see build/gui.ps1).
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$env:CGO_ENABLED = "0"

Write-Host "[1/8] gofmt check (GUI + B/S code)"
# Pre-existing CLI/provider files have historical formatting differences and
# are intentionally left untouched.
$unformatted = gofmt -l ./cmd/db-fly-gui ./cmd/db-fly-server ./internal/bridge ./internal/bootstrap ./internal/auth ./internal/httpapi ./internal/webui/embed.go
if ($unformatted) {
    Write-Error "Unformatted files:`n$unformatted"
}

Write-Host "[2/8] default build (pure Go, no gcc)"
go build ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[3/8] go vet"
go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[4/8] unit tests"
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[5/8] frontend type-check"
Push-Location frontend
if (-not (Test-Path node_modules)) { npm install }
npm run typecheck
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Write-Host "[6/8] frontend build (GUI template)"
Push-Location frontend
npm run build
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Write-Host "[7/8] frontend build (Web template)"
Remove-Item -Recurse -Force frontend/dist-web -ErrorAction SilentlyContinue
Push-Location frontend
npm run build:web
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Write-Host "[8/8] Linux cross build of the B/S entry"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$probe = Join-Path $env:TEMP "db-fly-verify-server"
go build -trimpath -o $probe ./cmd/db-fly-server
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Remove-Item -Force $probe -ErrorAction SilentlyContinue

Write-Host "All checks passed (GUI local mode + B/S mode)"
