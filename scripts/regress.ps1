<#
.SYNOPSIS
  AgentGo 回归基线(M0):Go 单测、web 构建、(若装有)Win7 旧版 Go 工具链的编译守卫。
.EXAMPLE
  .\scripts\regress.ps1
#>
param(
    # Legacy Go toolchain that builds the Windows 7 release (go-legacy-win7): full path to its bin\go.exe.
    # Default: the AGENTGO_WIN7_GO environment variable. Without it the Win7 build guard below is skipped with a warning.
    [string]$Win7Go = $env:AGENTGO_WIN7_GO
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
    if ($Win7Go -and (Test-Path -LiteralPath $Win7Go)) {
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
        Write-Warning "legacy Win7 Go toolchain not found (set AGENTGO_WIN7_GO or pass -Win7Go; got '$Win7Go'), skipping the Win7 build guard"
    }
} finally { Pop-Location }

if ($failed.Count) { Write-Host "FAILED: $($failed -join ', ')" -ForegroundColor Red; exit 1 }
Write-Host "ALL OK" -ForegroundColor Green
