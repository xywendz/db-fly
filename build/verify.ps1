# Full verification without gcc: use Fyne's ci (headless) tag to type-check GUI code,
# then run vet and all unit tests. This does NOT produce a GUI executable.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$env:CGO_ENABLED = "0"

Write-Host "[1/5] gofmt check"
$unformatted = gofmt -l ./cmd ./internal
if ($unformatted) {
    Write-Error "Unformatted files:`n$unformatted"
}

Write-Host "[2/5] default build"
go build ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[3/5] GUI type-check (tags: ci cgo)"
go build -tags "ci cgo" ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[4/5] go vet"
go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "[5/5] unit tests"
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "All checks passed"
