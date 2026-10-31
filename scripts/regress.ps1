<#
.SYNOPSIS
  AgentGo 回归基线(M0):Go 单测 +(若存在)agentgo-crush 的 MCP 冒烟。
.EXAMPLE
  .\scripts\regress.ps1
  .\scripts\regress.ps1 -SkipMcp
#>
param(
    [switch]$SkipMcp,
    # Legacy Go toolchain that builds the Windows 7 release (go-legacy-win7).
    [string]$Win7Go = 'E:\tools\go-win7\go-legacy-win7\bin\go.exe'
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$failed = @()

Push-Location $root
try {
    Write-Host "== go test ./internal/... ./pkg/... =="
    go test ./internal/... ./pkg/...
    if ($LASTEXITCODE -ne 0) { $failed += 'go test' }

    # The browser build (-tags web) is what ships to Windows 7. Its host code sits
    # behind the `web` tag, so the plain `go test` above never compiles it.
    Write-Host "== go build -tags web ./... =="
    go build -tags web ./...
    if ($LASTEXITCODE -ne 0) { $failed += 'go build (web)' }

    Write-Host "== go test -tags web ./... =="
    go test -tags web ./internal/... ./pkg/... -count=1
    if ($LASTEXITCODE -ne 0) { $failed += 'go test (web)' }

    # The Windows 7 release is this same source built by the legacy Go toolchain
    # with cgo off (scripts/release-win7.ps1); there is no win7 build tag. Compile
    # it that way here too, so a dependency that needs cgo, or an import only the
    # newer Go can compile, shows up today and not on release day. Skipped when
    # that toolchain is not installed.
    if (Test-Path -LiteralPath $Win7Go) {
        Write-Host "== go build -tags web ./... (legacy Win7 toolchain, cgo off) =="
        $saved = @{ CGO_ENABLED = $env:CGO_ENABLED; GOTOOLCHAIN = $env:GOTOOLCHAIN; GOWORK = $env:GOWORK }
        $env:CGO_ENABLED = '0'; $env:GOTOOLCHAIN = 'local'; $env:GOWORK = 'off'
        try {
            & $Win7Go build -tags web ./...
            if ($LASTEXITCODE -ne 0) { $failed += 'go build (legacy Win7 toolchain)' }
        } finally {
            foreach ($k in $saved.Keys) {
                if ($null -eq $saved[$k]) { Remove-Item "Env:$k" -ErrorAction SilentlyContinue } else { Set-Item "Env:$k" $saved[$k] }
            }
        }
    } else {
        Write-Warning "legacy Win7 Go toolchain not found at '$Win7Go', skipping the Win7 build guard"
    }
} finally { Pop-Location }

if (-not $SkipMcp) {
    $crush = Join-Path (Split-Path -Parent $root) 'agentgo-crush'
    if (Test-Path (Join-Path $crush 'cmd\agentgo-mcp')) {
        Push-Location $crush
        try {
            Write-Host "== build agentgo-mcp =="
            go build -o agentgo-mcp.exe ./cmd/agentgo-mcp
            if ($LASTEXITCODE -ne 0) { $failed += 'build agentgo-mcp' }
            else {
                Write-Host "== MCP smoke =="
                python scripts/smoke_mcp.py
                if ($LASTEXITCODE -ne 0) { $failed += 'mcp smoke' }
            }
        } finally { Pop-Location }
    } else {
        Write-Warning "未找到 agentgo-crush,跳过 MCP 冒烟"
    }
}

if ($failed.Count) { Write-Host "FAILED: $($failed -join ', ')" -ForegroundColor Red; exit 1 }
Write-Host "ALL OK" -ForegroundColor Green
