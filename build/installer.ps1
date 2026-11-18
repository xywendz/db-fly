# Build the Windows installer from the GUI exe produced by build/gui.ps1.
# Requires NSIS 3 (makensis on PATH). Keep this script ASCII-only for PS 5.1.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$exe = "dist/db-fly-gui.exe"
if (-not (Test-Path $exe)) {
    throw "dist/db-fly-gui.exe not found. Run ./build/gui.ps1 first."
}

$makensis = Get-Command makensis -ErrorAction SilentlyContinue
if (-not $makensis) {
    throw "makensis not found. Install NSIS 3 and add it to PATH (https://nsis.sourceforge.io/)."
}

# Same version source as gui.ps1, so installer/About/update agree.
$version = "dev"
try { $version = (git describe --tags --always 2>$null).Trim() }
catch { }
if (-not $version) { $version = "dev" }

# NSIS version resource needs four numeric components (dev -> 0.0.0.0).
$numericVersion = "0.0.0.0"
$match = [regex]::Match($version, '(\d+)\.(\d+)\.(\d+)')
if ($match.Success) {
    $numericVersion = "{0}.{1}.{2}.0" -f $match.Groups[1].Value, $match.Groups[2].Value, $match.Groups[3].Value
}
$displayVersion = $version -replace '^v', ''

Push-Location (Join-Path $PSScriptRoot "windows")
try {
    & makensis "/DAPP_VERSION=$displayVersion" "/DAPP_VERSION_NUM=$numericVersion" installer.nsi
    if ($LASTEXITCODE -ne 0) { throw "makensis failed with exit code $LASTEXITCODE" }
}
finally {
    Pop-Location
}

$setup = "dist/db-fly-setup-windows-amd64.exe"
if (-not (Test-Path $setup)) { throw "Installer was not produced: $setup" }
$sizeMB = [math]::Round((Get-Item $setup).Length / 1MB, 1)
Write-Host "Built $setup ($sizeMB MB, version $version)"
