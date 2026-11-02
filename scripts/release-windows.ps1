param(
 [ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9._-]*$')]
 [string]$Version = '0.1.0-rc.20261024.1',
 [string]$Go = 'go',
 [string]$Npm = 'npm.cmd',
 [string]$Wails = 'wails3',
 [ValidateRange(1,16)]
 [int]$TestParallelism = 4,
 [switch]$IncludePrivateExtensions,
 [switch]$SkipTests,
 [string]$SourceRevision = '',
 [bool]$SourceDirty = $false,
 # Where the package folder and zip go (default: <repo>/bin). Use another drive when the repo drive is full.
 [string]$OutDir = ''
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$outRoot = if ($OutDir) { $OutDir } else { Join-Path $projectRoot 'bin' }
$releaseRoot = Join-Path $outRoot ("agentgo-" + $Version + "-windows-amd64")
$archive = $releaseRoot + '.zip'
$resource = Join-Path $projectRoot 'cmd/agentgo/wails_windows_amd64.syso'
foreach ($target in @($releaseRoot,$archive,$resource)) {
 if (Test-Path -LiteralPath $target) { throw "Target already exists; choose another version or remove your own old output: $target" }
}
# Git ignores do not control the Go compiler or Vite's eager glob imports.
$privateInputs = @(
 Get-ChildItem -Path (Join-Path $projectRoot 'internal/bridge/*_private*.go') -ErrorAction SilentlyContinue
 Get-ChildItem -Path (Join-Path $projectRoot 'frontend/src/features/settings/*.private.*') -ErrorAction SilentlyContinue
)
if ($privateInputs.Count -gt 0 -and -not $IncludePrivateExtensions) {
 throw 'Private extension source is present. Export a clean public source tree with scripts/prepare-public-source.py. Use -IncludePrivateExtensions only for your internal testing build.'
}
$previousWork = $env:GOWORK
$previousCGO = $env:CGO_ENABLED
$env:GOWORK = 'off'
$env:CGO_ENABLED = '0'
Push-Location $projectRoot
try {
 if (-not $SkipTests) {
  & $Go test "-p=$TestParallelism" ./... -count=1 -timeout=120s
  if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
  & $Npm --prefix frontend run typecheck
  if ($LASTEXITCODE -ne 0) { throw 'Frontend typecheck failed' }
  & $Npm --prefix frontend test
  if ($LASTEXITCODE -ne 0) { throw 'Frontend tests failed' }
 }
 & $Npm --prefix frontend run build
 if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }

 New-Item -ItemType Directory -Path $releaseRoot | Out-Null
 $executable = Join-Path $releaseRoot 'AgentGo.exe'
 # Built-in PPT templates are compiled in but not in the repository (internal/officedoc/builtin_tpl/README.txt).
 $builtinTemplates = @(Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot '..\internal\officedoc\builtin_tpl') -File -ErrorAction SilentlyContinue | Where-Object { $_.Extension -in '.pptx','.potx' })
 if ($builtinTemplates.Count -eq 0) { Write-Warning 'No built-in PPT template in internal/officedoc/builtin_tpl: this package will not have builtin:company.' }
 else { Write-Host ('Built-in PPT templates: ' + (($builtinTemplates | ForEach-Object { $_.BaseName }) -join ', ')) }
 try {
  & $Wails generate syso -arch amd64 -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info build/windows/info.json -out $resource
  if ($LASTEXITCODE -ne 0) { throw 'Windows resources failed' }
  # The version is stamped into the binary: Settings > Usage & diagnostics and the diagnostics bundle report it.
  & $Go build -tags production -trimpath -buildvcs=false "-ldflags=-w -s -H windowsgui -X agentgo/internal/buildinfo.Version=$Version" -o $executable ./cmd/agentgo
  if ($LASTEXITCODE -ne 0) { throw 'Production build failed' }
 } finally {
  if (Test-Path -LiteralPath $resource) { Remove-Item -LiteralPath $resource }
 }
 $bytes = [IO.File]::ReadAllBytes($executable)
 $pe = [BitConverter]::ToInt32($bytes,0x3c)
 if ([BitConverter]::ToUInt16($bytes,$pe+4) -ne 0x8664 -or [BitConverter]::ToUInt16($bytes,$pe+24+68) -ne 2) {
  throw 'Expected a Windows amd64 GUI executable'
 }
 $sha = (Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash
 $utf8 = [Text.UTF8Encoding]::new($false)
 [IO.File]::WriteAllText((Join-Path $releaseRoot 'SHA256SUMS.txt'),$sha+"  AgentGo.exe"+[Environment]::NewLine,$utf8)
 Copy-Item -LiteralPath (Join-Path $projectRoot 'packaging/release-windows-readme.md') -Destination (Join-Path $releaseRoot 'README.md')
 Copy-Item -LiteralPath (Join-Path $projectRoot 'packaging/user-hooks.md') -Destination (Join-Path $releaseRoot 'user-hooks.md')
 foreach ($name in @('LICENSE','LICENSE.txt','NOTICE')) {
  $source = Join-Path $projectRoot $name
  if (Test-Path -LiteralPath $source -PathType Leaf) { Copy-Item -LiteralPath $source -Destination (Join-Path $releaseRoot $name) }
 }
 if ($SourceRevision) { $head=$SourceRevision; $dirty=$SourceDirty }
 else {
  $head = (& git rev-parse HEAD).Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Unable to determine source revision' }
  $dirty = @(& git status --porcelain).Count -gt 0
 }
 $receipt = [ordered]@{
  audience=$(if ($IncludePrivateExtensions) {'private-internal-test'} elseif ($builtinTemplates.Count -gt 0) {'company-internal'} else {'public-candidate'}); private_extensions_included=[bool]$IncludePrivateExtensions
  builtin_ppt_templates=@($builtinTemplates | ForEach-Object { $_.BaseName })
  version=$Version; built_utc=[DateTime]::UtcNow.ToString('o'); source_commit=$head; source_dirty=$dirty
  go_version=(& $Go version); executable_sha256=$sha
  go_tests=$(if ($SkipTests) {'skipped'} else {'passed'}); test_package_parallelism=$TestParallelism; frontend_typecheck=$(if ($SkipTests) {'skipped'} else {'passed'}); frontend_tests=$(if ($SkipTests) {'skipped'} else {'passed'}); frontend_build='passed'
  target='windows-amd64-gui'; manual_ui_acceptance='pending'; live_model_acceptance='pending'; distribution_license_review='attributions-in-licenses'
 }
 [IO.File]::WriteAllText((Join-Path $releaseRoot 'build-info.json'),($receipt | ConvertTo-Json),$utf8)
 Copy-Item -LiteralPath (Join-Path $projectRoot 'licenses') -Destination (Join-Path $releaseRoot 'licenses') -Recurse
 $manifest = Join-Path $projectRoot 'public-source-manifest.json'
 if (Test-Path -LiteralPath $manifest) { Copy-Item -LiteralPath $manifest -Destination $releaseRoot }
 if ($IncludePrivateExtensions) {
  [IO.File]::WriteAllText((Join-Path $releaseRoot 'PRIVATE-BUILD.txt'),'Internal testing build. Contains local private extensions. Do not publish this executable.',$utf8)
 }
 $files = @(Get-ChildItem -LiteralPath $releaseRoot | ForEach-Object FullName)
 Compress-Archive -LiteralPath $files -DestinationPath $archive
 $zipSha = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
 [IO.File]::WriteAllText(($archive+'.sha256'),$zipSha+'  '+[IO.Path]::GetFileName($archive)+[Environment]::NewLine,$utf8)
 Write-Output ("RELEASE_EXE="+$executable)
 Write-Output ("RELEASE_ZIP="+$archive)
 Write-Output ("EXE_SHA256="+$sha)
 Write-Output ("ZIP_SHA256="+$zipSha)
} finally {
 Pop-Location
 $env:GOWORK=$previousWork
 $env:CGO_ENABLED=$previousCGO
}
