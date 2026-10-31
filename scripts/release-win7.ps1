param(
 [ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9._-]*$')]
 [string]$Version = '0.1.0-win7.1',
 # go-legacy-win7 toolchain (https://github.com/thongtech/go-legacy-win7). The stock Go 1.21+ cannot target Windows 7.
 [string]$Go = 'E:\tools\go-win7\go-legacy-win7\bin\go.exe',
 [string]$Npm = 'npm.cmd',
 [string]$GoCache = 'E:\tmp\gocache-win7',
 [ValidateRange(1,16)]
 [int]$TestParallelism = 2,
 [switch]$SkipTests,
 [switch]$IncludePrivateExtensions,
 # 386 builds the 32-bit package with go386.mod (replaces upstream sonic, which cannot compile on 32-bit).
 [ValidateSet('amd64','386')]
 [string]$Arch = 'amd64',
 # Where the package folder and zip go (default: <repo>/bin). Use another drive when the repo drive is full.
 [string]$OutDir = ''
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$outRoot = if ($OutDir) { $OutDir } else { Join-Path $projectRoot 'bin' }
$releaseRoot = Join-Path $outRoot ("agentgo-" + $Version + "-windows7-" + $Arch)
$archive = $releaseRoot + '.zip'
foreach ($target in @($releaseRoot,$archive)) {
 if (Test-Path -LiteralPath $target) { throw "Target already exists; choose another version or remove your own old output: $target" }
}
if (-not (Test-Path -LiteralPath $Go)) { throw "Legacy Go toolchain not found: $Go" }
$privateInputs = @(
 Get-ChildItem -Path (Join-Path $projectRoot 'internal/bridge/*_private*.go') -ErrorAction SilentlyContinue
 Get-ChildItem -Path (Join-Path $projectRoot 'frontend/src/features/settings/*.private.*') -ErrorAction SilentlyContinue
)
if ($privateInputs.Count -gt 0 -and -not $IncludePrivateExtensions) {
 throw 'Private extension source is present. Use -IncludePrivateExtensions only for your internal testing build.'
}
$saved = @{ GOWORK=$env:GOWORK; CGO_ENABLED=$env:CGO_ENABLED; GOTOOLCHAIN=$env:GOTOOLCHAIN; GOCACHE=$env:GOCACHE; PATH=$env:PATH; GOARCH=$env:GOARCH; GOOS=$env:GOOS }
$modArgs = @()
$testTargets = @('./...')
$expectedMachine = 0x8664
if ($Arch -eq '386') {
 if (-not (Test-Path -LiteralPath (Join-Path $projectRoot 'go386.mod'))) { throw 'go386.mod is missing' }
 $env:GOARCH = '386'; $env:GOOS = 'windows'
 $modArgs = @('-modfile=go386.mod')
 # The desktop shell (Wails) does not build on 32-bit and is not part of the browser package.
 $testTargets = @('./internal/...','./cmd/agentgo-web/...')
 $expectedMachine = 0x014C
}
$env:GOWORK = 'off'
$env:CGO_ENABLED = '0'
$env:GOTOOLCHAIN = 'local'   # never let go.mod switch to a newer, non-Win7 toolchain
$env:GOCACHE = $GoCache
$env:PATH = (Split-Path -Parent $Go) + ';' + $env:PATH
Push-Location $projectRoot
try {
 $goVersion = (& $Go version)
 if ($goVersion -notmatch 'go1\.') { throw "Unexpected go version output: $goVersion" }
 if (-not $SkipTests) {
  & $Go test @modArgs -tags web "-p=$TestParallelism" @testTargets -count=1 -timeout=180s
  if ($LASTEXITCODE -ne 0) { throw 'Go tests (-tags web) failed' }
  & $Npm --prefix frontend run typecheck
  if ($LASTEXITCODE -ne 0) { throw 'Frontend typecheck failed' }
  & $Npm --prefix frontend test
  if ($LASTEXITCODE -ne 0) { throw 'Frontend tests failed' }
 }
 & $Npm --prefix frontend run build
 if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }

 New-Item -ItemType Directory -Path $releaseRoot | Out-Null
 $executable = Join-Path $releaseRoot 'AgentGo-web.exe'
 # Built-in PPT templates are compiled in but not in the repository (internal/officedoc/builtin_tpl/README.txt).
 $builtinTemplates = @(Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot '..\internal\officedoc\builtin_tpl') -File -ErrorAction SilentlyContinue | Where-Object { $_.Extension -in '.pptx','.potx' })
 if ($builtinTemplates.Count -eq 0) { Write-Warning 'No built-in PPT template in internal/officedoc/builtin_tpl: this package will not have builtin:company.' }
 else { Write-Host ('Built-in PPT templates: ' + (($builtinTemplates | ForEach-Object { $_.BaseName }) -join ', ')) }
 # The version is stamped into the binary: Settings > Usage & diagnostics and the diagnostics bundle report it.
 & $Go build @modArgs -tags web -trimpath -buildvcs=false "-ldflags=-w -s -X agentgo/internal/buildinfo.Version=$Version" -o $executable ./cmd/agentgo-web
 if ($LASTEXITCODE -ne 0) { throw 'Win7 web build failed' }
 $bytes = [IO.File]::ReadAllBytes($executable)
 $pe = [BitConverter]::ToInt32($bytes,0x3c)
 if ([BitConverter]::ToUInt16($bytes,$pe+4) -ne $expectedMachine) { throw "Expected a Windows $Arch executable" }
 # The browser build must not contain the Wails/WebView2 runtime at all.
 $text = [Text.Encoding]::ASCII.GetString($bytes)
 if ($text.Contains('wailsapp/wails') -or $text.Contains('WebView2Loader')) { throw 'Wails/WebView2 leaked into the web build' }
 # The only thing this assembly must not carry is the local ONNX OCR engine (not
 # written yet; it will sit behind the `onnx` build tag). It needs cgo plus
 # onnxruntime.dll, and that DLL statically imports Win8+ APIs (CreateFile2,
 # GetSystemTimePreciseAsFileTime), so it cannot load on Windows 7 at all. The
 # knowledge base (internal/docchunk) is NOT excluded: it is pure Go, costs ~100 KB,
 # and is switched by AGENTGO_DOCCHUNK (off by default), not by a build tag.
 #
 # Assert on the artifact, not just on the build invocation: a stray import that
 # drags the engine back in would otherwise only show up as a larger binary. These
 # markers are armed in advance: nothing contains them today, so the check has not
 # yet been seen to fire. When the engine lands, build once with -tags onnx and
 # confirm both markers appear there before trusting this check.
 # Do NOT use user-visible phrases from internal/config/flags.go as markers: that
 # file is compiled into every build, so those strings are always present.
 $leaked = @()
 foreach ($marker in @('onnxruntime','ppocrv6')) {
  if ($text.Contains($marker)) { $leaked += $marker }
 }
 if ($leaked.Count -gt 0) { throw ('Local OCR engine leaked into the Win7 build: ' + ($leaked -join ', ')) }

 # Positive check for what must stay: OCR through a vision model on the gateway needs
 # no cgo and no local runtime, so it is supported on Win7 and must not be dropped by
 # accident.
 foreach ($marker in @('ocr_image','visionocr')) {
  if (-not $text.Contains($marker)) { throw "$marker is missing from the Win7 build (expected: gateway OCR is supported there)" }
 }

 $sha = (Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash
 $utf8 = [Text.UTF8Encoding]::new($false)
 [IO.File]::WriteAllText((Join-Path $releaseRoot 'SHA256SUMS.txt'),$sha+"  AgentGo-web.exe"+[Environment]::NewLine,$utf8)
 # ANSI/GBK-safe launcher: cmd.exe reads batch files in the system code page, so keep it ASCII.
 $bat = "@echo off`r`ncd /d `"%~dp0`"`r`nAgentGo-web.exe %*`r`nif errorlevel 1 pause`r`n"
 [IO.File]::WriteAllText((Join-Path $releaseRoot 'start-AgentGo.bat'),$bat,[Text.Encoding]::ASCII)
 Copy-Item -LiteralPath (Join-Path $projectRoot 'docs/release-win7-readme.md') -Destination (Join-Path $releaseRoot 'README.md')
 Copy-Item -LiteralPath (Join-Path $projectRoot 'docs/user-hooks.md') -Destination (Join-Path $releaseRoot 'user-hooks.md')
 foreach ($name in @('LICENSE','LICENSE.txt','NOTICE')) {
  $source = Join-Path $projectRoot $name
  if (Test-Path -LiteralPath $source -PathType Leaf) { Copy-Item -LiteralPath $source -Destination (Join-Path $releaseRoot $name) }
 }
 $head = (& git rev-parse HEAD).Trim()
 $dirty = @(& git status --porcelain).Count -gt 0
 $receipt = [ordered]@{
  audience=$(if ($IncludePrivateExtensions) {'private-internal-test'} elseif ($builtinTemplates.Count -gt 0) {'company-internal'} else {'public-candidate'}); private_extensions_included=[bool]$IncludePrivateExtensions
  builtin_ppt_templates=@($builtinTemplates | ForEach-Object { $_.BaseName })
  version=$Version; built_utc=[DateTime]::UtcNow.ToString('o'); source_commit=$head; source_dirty=$dirty
  go_version=$goVersion; go_toolchain='thongtech/go-legacy-win7'; executable_sha256=$sha
  go_tests=$(if ($SkipTests) {'skipped'} else {'passed'}); frontend_checks=$(if ($SkipTests) {'skipped'} else {'passed'}); frontend_build='passed'
  target=('windows7-' + $Arch + '-browser'); windows7_machine_acceptance='pending'
 }
 [IO.File]::WriteAllText((Join-Path $releaseRoot 'build-info.json'),($receipt | ConvertTo-Json),$utf8)
 Copy-Item -LiteralPath (Join-Path $projectRoot 'licenses') -Destination (Join-Path $releaseRoot 'licenses') -Recurse
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
 foreach ($k in $saved.Keys) { Set-Item -Path ("Env:" + $k) -Value $saved[$k] }
}
