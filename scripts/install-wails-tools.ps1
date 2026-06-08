# 将 Wails CLI 与 NSIS 安装到 D:\dev，并写入用户 PATH / Go 环境变量。
# 用法: powershell -ExecutionPolicy Bypass -File scripts/install-wails-tools.ps1

$ErrorActionPreference = "Stop"
$DevRoot = "D:\dev"
$GoBin = Join-Path $DevRoot "go\bin"
$GoModCache = Join-Path $DevRoot "go\pkg\mod"
$GoPath = Join-Path $DevRoot "go"
$NsisDir = Join-Path $DevRoot "nsis"

New-Item -ItemType Directory -Path $GoBin, $GoModCache -Force | Out-Null

Write-Host "==> NSIS -> $NsisDir"
if (-not (Test-Path (Join-Path $NsisDir "makensis.exe"))) {
    winget install --id NSIS.NSIS --accept-package-agreements --accept-source-agreements --location $NsisDir
} else {
    Write-Host "    NSIS already present, skip winget"
}

Write-Host "==> wails3 -> $GoBin"
$env:GOBIN = $GoBin
$env:GOMODCACHE = $GoModCache
$env:GOPATH = $GoPath
go install github.com/wailsapp/wails/v3/cmd/wails3@latest

function Add-UserPath([string]$dir) {
    $p = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($p -notlike "*$dir*") {
        [Environment]::SetEnvironmentVariable("Path", $(if ($p) { "$p;$dir" } else { $dir }), "User")
    }
}

Add-UserPath $GoBin
Add-UserPath $NsisDir
[Environment]::SetEnvironmentVariable("GOBIN", $GoBin, "User")
[Environment]::SetEnvironmentVariable("GOMODCACHE", $GoModCache, "User")
[Environment]::SetEnvironmentVariable("GOPATH", $GoPath, "User")

$env:Path = "$GoBin;$NsisDir;" + $env:Path
Write-Host ""
Write-Host "Done. Open a NEW terminal, then:"
Write-Host "  wails3 doctor"
Write-Host "  cd <agentgo> ; wails3 build ; wails3 package"
