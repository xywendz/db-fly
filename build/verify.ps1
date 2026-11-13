# Full verification: build (pure Go, no gcc needed), vet and unit tests for the
# Go side; type-check + build for the Vue frontend.
# This does NOT produce a GUI executable (see build/gui.ps1 / wails3 build).
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$env:CGO_ENABLED = "0"

Write-Host "[1/6] gofmt check (restructured code)"
# Only check the wails3 refactor code. Pre-existing CLI/provider files have
# historical formatting differences and are intentionally left untouched.
$unformatted = gofmt -l ./cmd/db-fly-gui ./internal/bridge
if ($unformatted) {
    Write-Error "Unformatted files:`n$unformatted"
}

Write-Host "[2/6] default build (pure Go, no gcc)"
go build ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[3/6] go vet"
go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[4/6] unit tests"
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[5/6] frontend type-check"
Push-Location frontend
if (-not (Test-Path node_modules)) { npm install }
npm run typecheck
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Pop-Location

Write-Host "[6/6] frontend build"
Push-Location frontend
npm run build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Pop-Location

Write-Host "All checks passed"
