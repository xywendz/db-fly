# Build the Windows GUI.
# Requirements: mingw-w64 gcc in PATH (Fyne needs CGO + OpenGL toolchain).
# Optional: use `fyne package` to produce an installer with an icon.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$version = if ($env:VERSION) { $env:VERSION } else { "dev" }
$env:CGO_ENABLED = "1"
New-Item -ItemType Directory -Force -Path dist | Out-Null

go build -trimpath `
  -ldflags "-s -w -H windowsgui -X github.com/dbclient/dbclient/internal/cli.Version=$version" `
  -o dist/dbclient-gui.exe ./cmd/dbclient-gui

Write-Host "Built dist/dbclient-gui.exe ($version)"
