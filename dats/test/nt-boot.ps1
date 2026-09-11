# see docs/CI.md "test job" for why Start-Process + $p.ExitCode is used here instead of pwsh native invocation.
$ErrorActionPreference = 'Stop'
$failed = $false
$cases = @(
  @{ Args = @('10', '5'); Want = "fizzbuzz`n" },
  @{ Args = @('7', '6');  Want = "13`n" }
)
foreach ($origin in @('Linux', 'Windows')) {
  # Throwaway copy: keeps the downloaded artifact pristine (APE binaries self-assimilate on unix hosts).
  $src = "binaries/ape-binary-$origin/fizzbuzz.com"
  $copy = Join-Path $env:RUNNER_TEMP "fizzbuzz-$origin.com"
  Copy-Item $src $copy
  foreach ($case in $cases) {
    $out = Join-Path $env:RUNNER_TEMP "stdout-$origin-$($case.Args -join '-').txt"
    Write-Host "${origin}: launching $copy $($case.Args -join ' ')"
    $p = Start-Process -FilePath $copy -ArgumentList $case.Args -Wait -PassThru -NoNewWindow -RedirectStandardOutput $out
    $got = [System.IO.File]::ReadAllText($out)
    Write-Host "${origin}: exit code $($p.ExitCode) (want 0), stdout $($got | ConvertTo-Json)"
    if ($p.ExitCode -ne 0) { $failed = $true }
    if ($got -cne $case.Want) {
      Write-Host "${origin}: stdout mismatch: got $($got | ConvertTo-Json), want $($case.Want | ConvertTo-Json)"
      $failed = $true
    }
  }
}
if ($failed) { exit 1 } else { exit 0 }

      # Prefetched separately so a stalled module download can't masquerade as a hung test step.
