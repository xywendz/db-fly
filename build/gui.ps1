# Build the Windows GUI.
# Fyne requires CGO + a mingw-w64 gcc. Without them go build silently picks
# cmd/db-fly-gui/main_stub.go, which prints a message and exits immediately;
# with "-H windowsgui" no console is attached, so the exe appears "unopenable".
# NOTE: keep this script ASCII-only. Windows PowerShell 5.1 decodes BOM-less
# UTF-8 .ps1 files as ANSI, which corrupts non-ASCII text and breaks parsing.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

# Go defaults to CGO_ENABLED=0 when no C compiler is on PATH; enable it explicitly.
$env:CGO_ENABLED = "1"
if ((go env CGO_ENABLED) -ne "1") {
    throw "Failed to enable CGO_ENABLED=1; cannot build the GUI."
}

# Locate a mingw-w64 gcc, including common MSYS2/TDM/scoop install locations.
$gcc = Get-Command gcc -ErrorAction SilentlyContinue
if (-not $gcc) {
    $candidates = @(
        "C:\msys64\ucrt64\bin\gcc.exe",
        "C:\msys64\mingw64\bin\gcc.exe",
        "C:\TDM-GCC-64\bin\gcc.exe",
        "C:\mingw64\bin\gcc.exe",
        "C:\MinGW\bin\gcc.exe",
        "$env:LOCALAPPDATA\scoop\apps\mingw\current\bin\gcc.exe"
    )
    $found = $candidates | Where-Object { Test-Path $_ } | Select-Object -First 1
    if (-not $found) {
        throw "mingw-w64 gcc not found. Install MSYS2 (pacman -S mingw-w64-ucrt-x86_64-gcc) or TDM-GCC and add its bin directory to PATH, then retry."
    }
    $env:PATH = (Split-Path -Parent $found) + ";" + $env:PATH
    Write-Host "Using gcc: $found"
}

New-Item -ItemType Directory -Force -Path dist | Out-Null

# -X ...internal/cli.Version is intentionally omitted: the GUI does not link
# internal/cli, so the linker silently drops that injection. Use build/cli.sh
# (or the dbctl build command) to set the CLI version.
go build -trimpath `
  -ldflags "-s -w -H windowsgui" `
  -o dist/db-fly-gui.exe ./cmd/db-fly-gui
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# Guard against producing a silent stub: verify the binary was built with cgo.
$bin = (Resolve-Path .\dist\db-fly-gui.exe).Path
$buildInfo = (go version -m $bin) -join "`n"
if ($buildInfo -notmatch "CGO_ENABLED=1") {
    Remove-Item -Force $bin
    throw "The binary was built without CGO (stub that exits silently on double-click). Check gcc and CGO_ENABLED, then rebuild."
}

Write-Host "Built dist/db-fly-gui.exe (CGO_ENABLED=1)"
