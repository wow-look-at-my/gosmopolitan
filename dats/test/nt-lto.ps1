# cmd/cgo/internal/swig links its callback program with -flto and the result
# will not start: NT answers ERROR_BAD_EXE_FORMAT. That link runs gcc, so the
# question is whether gcc writes a runnable image under -flto on this host at
# all, or only refuses the object this fork hands it.
#
# Two C files, so the link has something to do across translation units.
# see docs/CI.md "test job" for why Start-Process + $p.ExitCode is used here.
$ErrorActionPreference = 'Stop'

$gcc = (Get-Command gcc -ErrorAction SilentlyContinue).Source
if (-not $gcc) {
  Write-Host 'mingw gcc: not on this host'
  exit 0
}

$dir = Join-Path $env:RUNNER_TEMP 'ltoprobe'
New-Item -ItemType Directory -Force -Path $dir | Out-Null

Set-Content -Path (Join-Path $dir 'other.c') -Value @'
int answer(void) { return 0; }
'@
Set-Content -Path (Join-Path $dir 'main.c') -Value @'
#include <stdio.h>
int answer(void);
int main(void) { printf("OK\n"); return answer(); }
'@

$failed = $false
foreach ($case in @(
  @{ Name = 'plain'; Flags = @('-O2') },
  @{ Name = 'lto'; Flags = @('-O2', '-flto') }
)) {
  $exe = Join-Path $dir "$($case.Name).exe"
  $build = Start-Process -FilePath $gcc -Wait -PassThru -NoNewWindow `
    -ArgumentList ($case.Flags + @((Join-Path $dir 'main.c'), (Join-Path $dir 'other.c'), '-o', $exe))
  if ($build.ExitCode -ne 0) {
    Write-Host "gcc $($case.Name): build failed, exit $($build.ExitCode)"
    $failed = $true
    continue
  }
  $size = (Get-Item $exe).Length
  try {
    $proc = Start-Process -FilePath $exe -Wait -PassThru -NoNewWindow `
      -RedirectStandardOutput (Join-Path $dir "$($case.Name).txt")
    $code = $proc.ExitCode
  } catch {
    $code = "start failed: $($_.Exception.Message)"
    $failed = $true
  }
  Write-Host "gcc $($case.Name): $size bytes, exit $code"
  if ($code -ne 0) { $failed = $true }
}

if ($failed) { exit 1 } else { exit 0 }
