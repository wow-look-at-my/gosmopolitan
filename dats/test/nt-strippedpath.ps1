# os/exec's TestCommand sets PATH to a single dot and starts a copy of its own
# test binary. Every case that starts one fails on this host with exit code
# -1073741515, STATUS_DLL_NOT_FOUND, and no output. The binaries import
# kernel32 and nothing else, so this measures the other half: whether a
# program this fork never built behaves the same way here.
#
# The inherited case is the assertion. The others print what the host does, so
# the log says whether a stripped PATH stops any program or only ours.
# see docs/CI.md "test job" for why Start-Process + $p.ExitCode is used here.
$ErrorActionPreference = 'Stop'

$dir = Join-Path $env:RUNNER_TEMP 'strippedpath'
New-Item -ItemType Directory -Force -Path $dir | Out-Null

# Copied the way the test copies its own binary, and run from that copy.
$system = Join-Path $env:SystemRoot 'System32\whoami.exe'
$copy = Join-Path $dir 'whoami.exe'
Copy-Item $system $copy -Force

$failed = $false
foreach ($case in @(
  @{ Name = 'inherited'; Path = $env:PATH; Assert = $true },
  @{ Name = 'dot'; Path = '.'; Assert = $false },
  @{ Name = 'empty'; Path = ''; Assert = $false }
)) {
  $out = Join-Path $dir "out-$($case.Name).txt"
  $saved = $env:PATH
  $env:PATH = $case.Path
  try {
    $proc = Start-Process -FilePath $copy -Wait -PassThru -NoNewWindow -RedirectStandardOutput $out
    $code = $proc.ExitCode
  } catch {
    $code = "start failed: $($_.Exception.Message)"
  } finally {
    $env:PATH = $saved
  }
  Write-Host "system binary from a copy, PATH=$($case.Name): exit $code"
  if ($case.Assert -and $code -ne 0) { $failed = $true }
}

if ($failed) { exit 1 } else { exit 0 }
