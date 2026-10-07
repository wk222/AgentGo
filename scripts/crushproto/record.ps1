<#
.SYNOPSIS
  Record the real Crush client<->server /v1 traffic (for building the AgentGo protocol adapter).
.DESCRIPTION
  Starts a real `crush server`, a recording proxy in front of it, runs `crush run` through the
  proxy and auto-approves permission requests. Output: <Out>\rec.jsonl (+ openapi.json).
  Needs AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT (referenced as $VARS, never written).
.EXAMPLE
  .\scripts\crushproto\record.ps1 -Prompt "Create hello.txt containing hi, then read it back." -Out D:\dev\go\tmp\crushproto-ref
#>
param(
  [string]$Prompt = "Create a file named hello.txt containing the single word hi, then read it back and tell me its content.",
  [string]$Out = "D:\dev\go\tmp\crushproto-ref",
  [string]$Crush = (Join-Path (Split-Path -Parent (Split-Path -Parent $PSScriptRoot | Split-Path -Parent)) "crush\crush.exe"),
  [string]$Deployment = "gpt-6-luna",
  [int]$TimeoutSec = 150,
  [string]$Fixtures = ""   # also capture GET fixtures into this dir (e.g. internal\crushproto\testdata\fixtures)
)
$ErrorActionPreference = 'Continue'
if (-not $env:AZURE_OPENAI_API_KEY -or -not $env:AZURE_OPENAI_API_ENDPOINT) { throw "AZURE_OPENAI_API_KEY / AZURE_OPENAI_API_ENDPOINT not set" }
if (-not (Test-Path $Crush)) { throw "crush.exe not found: $Crush" }

Get-Process crush -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
if (Test-Path $Out) { Remove-Item -Recurse -Force $Out }
$cfg = Join-Path $Out "cfg"; $dat = Join-Path $Out "data"; $gd = Join-Path $Out "gdata"; $ws = Join-Path $Out "ws"
New-Item -ItemType Directory -Force $cfg, $dat, $gd, $ws | Out-Null

$conf = @{ providers = @{ azure = @{ api_key = '$AZURE_OPENAI_API_KEY'; api_endpoint = '$AZURE_OPENAI_API_ENDPOINT';
  models = @(@{ id = $Deployment; name = $Deployment; context_window = 400000; default_max_tokens = 16384 }) } };
  models = @{ large = @{ provider = "azure"; model = $Deployment }; small = @{ provider = "azure"; model = $Deployment } } } | ConvertTo-Json -Depth 8
[System.IO.File]::WriteAllText((Join-Path $cfg "crush.json"), $conf, (New-Object System.Text.UTF8Encoding($false)))

$env:CRUSH_GLOBAL_CONFIG = $cfg; $env:CRUSH_GLOBAL_DATA = $gd
if (-not $env:AZURE_OPENAI_API_VERSION) { $env:AZURE_OPENAI_API_VERSION = "2025-04-01-preview" }
$rec = Join-Path $Out "rec.jsonl"

$srv = Start-Process $Crush -ArgumentList @("server", "-H", "tcp://127.0.0.1:18765", "-D", $dat, "-c", $ws) -PassThru -WindowStyle Hidden -RedirectStandardError "$Out\server.err" -RedirectStandardOutput "$Out\server.out"
$px = Start-Process python -ArgumentList @((Join-Path $PSScriptRoot "rec_proxy.py"), "--listen", "18766", "--upstream", "18765", "--out", $rec) -PassThru -WindowStyle Hidden
Start-Sleep 4
try { Invoke-WebRequest -UseBasicParsing http://127.0.0.1:18765/v1/docs/openapi.json -OutFile (Join-Path $Out "openapi.json") } catch { Write-Warning "openapi fetch failed" }

$env:CRUSH_CLIENT_SERVER = "1"
$job = Start-Process $Crush -ArgumentList @("run", "-H", "tcp://127.0.0.1:18766", "-D", $dat, $Prompt) -WorkingDirectory $ws -PassThru -WindowStyle Hidden -RedirectStandardOutput "$Out\run.out" -RedirectStandardError "$Out\run.err"

$granted = @{}
$deadline = (Get-Date).AddSeconds($TimeoutSec)
while (-not $job.HasExited -and (Get-Date) -lt $deadline) {
  Start-Sleep 1
  if (-not (Test-Path $rec)) { continue }
  foreach ($l in (Get-Content $rec -ErrorAction SilentlyContinue)) {
    if ($l -notmatch 'permission_request') { continue }
    $r = $l | ConvertFrom-Json
    if (-not $r.sse_of) { continue }
    foreach ($m in [regex]::Matches($r.data, 'data: (\{.*?\})\r?\n\r?\n')) {
      $ev = $m.Groups[1].Value | ConvertFrom-Json
      if ($ev.type -ne 'permission_request') { continue }
      $perm = $ev.payload.payload
      if ($granted[$perm.id]) { continue }
      $granted[$perm.id] = $true
      $wsid = ($r.sse_of -split '/')[3]
      $body = @{ permission = $perm; action = "allow" } | ConvertTo-Json -Depth 8 -Compress
      try { Invoke-WebRequest -UseBasicParsing -Method Post -Uri "http://127.0.0.1:18766/v1/workspaces/$wsid/permissions/grant" -ContentType "application/json" -Body $body | Out-Null; Write-Host "granted $($perm.tool_name)" } catch { Write-Warning $_.Exception.Message }
    }
  }
}
if (-not $job.HasExited) { Write-Warning "timeout; killing"; Stop-Process -Id $job.Id -Force -ErrorAction SilentlyContinue }
Start-Sleep 2
if ($Fixtures) {
  python (Join-Path $PSScriptRoot "capture_fixtures.py") --port 18765 --workdir $ws --datadir $dat --out $Fixtures
}
Stop-Process -Id $px.Id, $srv.Id -Force -ErrorAction SilentlyContinue
Write-Host ("recorded lines: " + (Get-Content $rec | Measure-Object).Count)
Write-Host "run output:"; Get-Content "$Out\run.out" -ErrorAction SilentlyContinue | Select-Object -First 20
Write-Host "workspace files:"; Get-ChildItem $ws | Select-Object -ExpandProperty Name
