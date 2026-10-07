<#
.SYNOPSIS
  AgentGo 回归基线(M0):Go 单测 +(若存在)agentgo-crush 的 MCP 冒烟。
.EXAMPLE
  .\scripts\regress.ps1
  .\scripts\regress.ps1 -SkipMcp
#>
param([switch]$SkipMcp)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$failed = @()

Push-Location $root
try {
    Write-Host "== go test ./internal/... ./pkg/... =="
    go test ./internal/... ./pkg/...
    if ($LASTEXITCODE -ne 0) { $failed += 'go test' }
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
