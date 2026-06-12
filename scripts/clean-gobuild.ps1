# AgentGo: reclaim disk from Go build cache and stray binaries.
# Usage: powershell -File scripts/clean-gobuild.ps1
# Tip: close other Go builds / IDE compiles if "Access is denied" on GOCACHE.

$ErrorActionPreference = "Continue"
$root = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $root "go.mod"))) {
    Write-Error "go.mod not found under $root"
    exit 1
}
Set-Location $root
Write-Host "Cleaning in $root"

$removed = 0
foreach ($name in @("agentgo.exe", "agentgo_build.exe", "agentgo_new.exe", "agentgo.exe~")) {
    $p = Join-Path $root $name
    if (Test-Path $p) {
        Remove-Item -Force $p
        Write-Host "  removed $name"
        $removed++
    }
}
if (Test-Path (Join-Path $root "bin")) {
    Get-ChildItem (Join-Path $root "bin") -Filter *.exe -ErrorAction SilentlyContinue | ForEach-Object {
        Remove-Item -Force $_.FullName
        Write-Host "  removed bin\$($_.Name)"
        $removed++
    }
}

go clean -testcache 2>&1 | Out-Null
$cacheOk = $true
go clean -cache 2>&1 | ForEach-Object {
    if ($_ -match "Access is denied") { $script:cacheOk = $false; Write-Warning $_ }
    else { Write-Host $_ }
}
if (-not $cacheOk) {
    Write-Warning "GOCACHE locked. Close Cursor/terminals running 'go test/build', then re-run this script."
}

go clean ./... 2>&1 | Out-Null
Write-Host "Done. Removed $removed binary file(s). Use: go build -o bin/agentgo.exe ./cmd/agentgo"
