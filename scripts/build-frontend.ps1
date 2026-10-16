# Builds the Vue frontend into frontend/dist.
# dist/ is a build product and is not tracked in git; the desktop app embeds it
# (frontend/frontend.go), so run this before `go build ./cmd/agentgo` when the
# UI changed. Equivalent to `task build:frontend` from build/Taskfile.yml.
$ErrorActionPreference = 'Stop'
$frontend = Join-Path (Split-Path $PSScriptRoot -Parent) 'frontend'
Push-Location $frontend
try {
    if (-not (Test-Path 'node_modules')) {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'vite build failed' }
    Write-Host "frontend built: $(Join-Path $frontend 'dist')"
}
finally {
    Pop-Location
}
