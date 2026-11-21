<#
.SYNOPSIS
  Vaultix agent one-shot setup for Windows.

.DESCRIPTION
  Installs the agent binary, enrolls this machine with the manager, and
  starts the Windows service (run as Administrator):

    Set-ExecutionPolicy Bypass -Scope Process -Force
    .\worker-windows.ps1 -Manager https://manager:8443 -Token <token>

  The -Token value is the SINGLE-USE enroll token issued by the manager
  (POST /api/agents/enroll-tokens). This script redeems it
  (POST /api/agent/enroll with this host's name) and persists the
  resulting agent credentials via `vaultix-agent.exe enroll` — the enroll
  token itself is never stored.

  Re-running with a FRESH enroll token re-enrolls this machine (config +
  binary replaced); re-running with an already-redeemed token fails loudly
  at the redeem step with the manager's 401.
#>
[CmdletBinding()]
param(
  [string]$Manager = $env:MANAGER_URL,
  [string]$Token = $env:VAULTIX_ENROLL_TOKEN,
  [string]$Hostname = "",
  [string]$Version = "latest",
  [switch]$DryRun
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
# Older Windows defaults to TLS < 1.2, which GitHub rejects.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = "doko89/vaultix"
$InstallDir = "C:\Program Files\Vaultix"
$DataDir = "C:\ProgramData\Vaultix"
$ServiceName = "vaultix-agent"

function Fail([string]$msg) {
  Write-Host "ERROR: $msg" -ForegroundColor Red
  exit 1
}

if ([string]::IsNullOrWhiteSpace($Version)) { Fail "--Version must not be empty" }
if ($Version -ne "latest" -and -not $Version.StartsWith("v")) { $Version = "v$Version" }

$Arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { Fail ("unsupported architecture: {0} (releases ship amd64 and arm64 only)" -f $_) }
}

if ([string]::IsNullOrWhiteSpace($Hostname)) { $Hostname = $env:COMPUTERNAME }

if ($DryRun) {
  Write-Host "DRY RUN — no changes will be made."
  Write-Host ("  manager:   {0}" -f ($(if ($Manager) { $Manager } else { "<missing: -Manager or MANAGER_URL>" })))
  Write-Host ("  token:     {0}" -f ($(if ($Token) { "<provided>" } else { "<missing: -Token or VAULTIX_ENROLL_TOKEN>" })))
  Write-Host "  hostname:  $Hostname"
  Write-Host "  version:   $Version"
  Write-Host "  asset:     https://github.com/$Repo/releases/download/$Version/vaultix-agent-windows-$Arch.exe"
  Write-Host "  steps:     check admin, download + sha256-verify agent binary to"
  Write-Host "             $InstallDir, redeem enroll token at POST /api/agent/enroll,"
  Write-Host "             persist credentials in $DataDir\agent.yaml, create + start"
  Write-Host "             the $ServiceName Windows service"
  if ([string]::IsNullOrWhiteSpace($Manager)) { Fail "dry-run: -Manager / MANAGER_URL is required" }
  if ([string]::IsNullOrWhiteSpace($Token)) { Fail "dry-run: -Token / VAULTIX_ENROLL_TOKEN is required" }
  exit 0
}

if ([string]::IsNullOrWhiteSpace($Manager)) { Fail "-Manager / MANAGER_URL is required" }
if ([string]::IsNullOrWhiteSpace($Token)) { Fail "-Token / VAULTIX_ENROLL_TOKEN is required" }
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  Fail "must run as Administrator"
}

# --- resolve version ---
$Tag = $Version
if ($Version -eq "latest") {
  Write-Host "resolving latest release..."
  try {
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
    $Tag = $rel.tag_name
  } catch {
    Fail ("could not resolve the latest release (network or no releases yet): {0}" -f $_.Exception.Message)
  }
  if ([string]::IsNullOrWhiteSpace($Tag)) { Fail "latest release has no tag" }
  Write-Host "latest is $Tag"
}

# --- download + verify agent binary ---
$Asset = "vaultix-agent-windows-$Arch.exe"
$work = Join-Path ([IO.Path]::GetTempPath()) ("vaultix-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $work | Out-Null
try {
  Write-Host "downloading $Asset ($Tag)..."
  Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$Tag/$Asset" -OutFile (Join-Path $work $Asset) -UseBasicParsing
  Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$Tag/sha256sums.txt" -OutFile (Join-Path $work "sha256sums.txt") -UseBasicParsing
} catch {
  Fail ("could not download {0} for {1}: {2}" -f $Asset, $Tag, $_.Exception.Message)
}
$line = Get-Content (Join-Path $work "sha256sums.txt") | Where-Object { $_ -match [regex]::Escape($Asset) + "$" } | Select-Object -First 1
if (-not $line) { Fail "sha256sums.txt has no entry for $Asset (refusing to install)" }
$want = ($line -split "\s+")[0].ToLower()
$got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $work $Asset)).Hash.ToLower()
if ($want -ne $got) { Fail "checksum verification FAILED for $Asset (refusing to install)" }
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
# A running service locks its binary: stop it before replacing (re-runs).
$runningSvc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($runningSvc -and $runningSvc.Status -eq "Running") {
  Write-Host "stopping existing $ServiceName for binary replace..."
  Stop-Service -Name $ServiceName -Force
}
Copy-Item (Join-Path $work $Asset) (Join-Path $InstallDir "vaultix-agent.exe") -Force
Remove-Item -Recurse -Force $work
$exe = Join-Path $InstallDir "vaultix-agent.exe"
Write-Host "installed $exe"

# --- redeem the enroll token ---
# POST /api/agent/enroll -> 201 {"agent_id","token",...}. Single-use token;
# the RESPONSE credential is what the agent persists. Never printed.
Write-Host "redeeming enroll token at $Manager (hostname: $Hostname, windows/$Arch)..."
$payload = @{ token = $Token; hostname = $Hostname; os = "windows"; architecture = $Arch } | ConvertTo-Json
try {
  $join = Invoke-RestMethod -Method Post -Uri "$Manager/api/agent/enroll" -ContentType "application/json" -Body $payload -UseBasicParsing
} catch {
  Fail ("enroll redeem failed — the token may be wrong, expired, or already redeemed (tokens are single-use; issue a fresh one): {0}" -f $_.Exception.Message)
}
if ([string]::IsNullOrWhiteSpace($join.agent_id) -or [string]::IsNullOrWhiteSpace($join.token)) {
  Fail "enroll response was missing agent_id/token"
}
Write-Host ("redeemed: agent {0}" -f $join.agent_id)

# --- persist agent config ---
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir "work") | Out-Null
$cfg = Join-Path $DataDir "agent.yaml"
$env:VAULTIX_AGENT_STATE_FILE = Join-Path $DataDir "agent-state.json"
$env:VAULTIX_AGENT_WORK_DIR = Join-Path $DataDir "work"
& $exe enroll --config $cfg --manager $Manager --token $join.token --agent-id $join.agent_id
if ($LASTEXITCODE -ne 0) { Fail "vaultix-agent enroll failed" }
icacls $cfg /inheritance:r /grant:r "SYSTEM:(R)" "Administrators:(R)" | Out-Null
Write-Host "wrote $cfg (restricted ACL)"

# --- windows service ---
$binPath = "`"$exe`" run --config `"$cfg`""
$existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existing) {
  Write-Host "service exists; updating binary path and restarting..."
  Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
  & sc.exe config $ServiceName binPath= $binPath | Out-Null
} else {
  & sc.exe create $ServiceName binPath= $binPath start= auto obj= LocalSystem | Out-Null
  if ($LASTEXITCODE -ne 0) { Fail "sc.exe create failed" }
  & sc.exe description $ServiceName "Vaultix Agent - per-VM backup executor" | Out-Null
  & sc.exe failure $ServiceName reset= 300 actions= restart/10000/restart/10000/restart/60000 | Out-Null
}
Start-Service -Name $ServiceName
Start-Sleep -Seconds 3
$svc = Get-Service -Name $ServiceName
if ($svc.Status -ne "Running") { Fail "$ServiceName is not running — inspect $DataDir\agent.log" }

Write-Host ""
Write-Host "======================================================================"
Write-Host "Vaultix agent $Tag enrolled as $($join.agent_id) and running."
Write-Host "  manager:  $Manager"
Write-Host "  hostname: $Hostname"
Write-Host "  config:   $cfg"
Write-Host "======================================================================"
Write-Host ""
Write-Host "Follow the agent log with:"
Write-Host ("  Get-Content {0}\agent.log -Wait -Tail 50" -f $DataDir)
