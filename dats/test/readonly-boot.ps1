# An APE boots on an NT host with nothing writable. This host needs no loader
# at all: the APE is a valid PE, and the OS maps the payload straight out of
# it. So the only thing to prove is that no write is needed.
#
# icacls denies this user every write on the program's directory and on the
# one TEMP and TMP point at. A canary write must fail before the run counts.
$ErrorActionPreference = 'Stop'

$work = Join-Path $env:RUNNER_TEMP "ro-boot-$PID"
$prog = Join-Path $work 'prog'
$nowrite = Join-Path $work 'nowrite'
New-Item -ItemType Directory -Force -Path $prog, $nowrite | Out-Null

$src = 'binaries/ape-binary-Windows/fizzbuzz.com'
if (-not (Test-Path $src)) { throw "$src is missing" }
$ape = Join-Path $prog 'prog.com'
Copy-Item $src $ape

# The identity this script runs as, denied every write on both directories.
$me = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
foreach ($d in @($prog, $nowrite)) {
  & icacls $d /deny "${me}:(W,D,WD,AD,WEA,WA)" /t /c | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "icacls could not deny writes on $d" }
}

# Prove the denial before trusting anything the run reports.
$canary = Join-Path $nowrite 'canary'
$writable = $true
try { [System.IO.File]::WriteAllText($canary, 'x') } catch { $writable = $false }
if ($writable) {
  & icacls $prog /reset /t /c | Out-Null
  & icacls $nowrite /reset /t /c | Out-Null
  throw 'the directory is writable, so this case proves nothing'
}

$out = Join-Path $env:RUNNER_TEMP "ro-boot-stdout-$PID.txt"
$code = $null
try {
  # TEMP and TMP point somewhere unwritable, so a run that needs scratch space
  # fails here rather than passing on the runner's own writable temp.
  $env:TEMP = $nowrite
  $env:TMP = $nowrite
  $p = Start-Process -FilePath $ape -ArgumentList @('10', '5') -Wait -PassThru -NoNewWindow -RedirectStandardOutput $out
  $code = $p.ExitCode
  $got = [System.IO.File]::ReadAllText($out)
} finally {
  & icacls $prog /reset /t /c | Out-Null
  & icacls $nowrite /reset /t /c | Out-Null
  Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}

Write-Host "read-only NT boot: exit $code, stdout $($got | ConvertTo-Json)"
if ($code -ne 0) { throw "a read-only NT host must still run the program, got exit $code" }
if ($got -cne "fizzbuzz`n") { throw "stdout mismatch: got $($got | ConvertTo-Json), want fizzbuzz" }
Write-Host 'read-only NT boot: fizzbuzz'
