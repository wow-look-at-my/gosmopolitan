# Report free disk, free memory, CPU utilisation and queue depth every half
# minute, in the shape .github/resource-log.sh prints on a /proc or BSD host.
# A runner that dies mid-job leaves no step after it to ask, so the last line
# printed is the only account of what the machine had left.
#
# NT keeps no load average. The processor queue length is what it answers
# instead: the threads ready to run and waiting for a processor, which is the
# number a load average is measuring on the other hosts.
#
# The counters come from CIM rather than Get-Counter, whose paths are
# localized: an image in another language answers nothing for an English path.
#
# A probe, not a check: it asserts nothing and never fails the build.

$ErrorActionPreference = 'Continue'

# megabytes renders a byte count the way df -Pm reports one.
function megabytes($bytes) {
	if ($null -eq $bytes) { return '-' }
	return [int]([double]$bytes / 1MB)
}

# freeOn answers the free megabytes of the volume holding path, or a dash when
# the volume cannot be read.
function freeOn($path) {
	try {
		$root = [System.IO.Path]::GetPathRoot((Resolve-Path -LiteralPath $path -ErrorAction Stop))
		$drive = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='$($root.TrimEnd('\'))'" -ErrorAction Stop
		return megabytes $drive.FreeSpace
	} catch {
		return '-'
	}
}

$cores = try { (Get-CimInstance Win32_ComputerSystem).NumberOfLogicalProcessors } catch { '-' }
Write-Host "resource cores $cores"

$workspace = if ($env:GITHUB_WORKSPACE) { $env:GITHUB_WORKSPACE } else { '.' }

while ($true) {
	$now = (Get-Date).ToUniversalTime().ToString('HH:mm:ss')
	$root = freeOn $env:SystemDrive
	$work = freeOn $workspace
	$free = try { megabytes ((Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory * 1KB) } catch { '-' }
	$busy = try {
		(Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor -Filter "Name='_Total'").PercentProcessorTime
	} catch { $null }
	$queue = try {
		(Get-CimInstance Win32_PerfFormattedData_PerfOS_System).ProcessorQueueLength
	} catch { $null }
	$cpu = if ($null -eq $busy) { '-' } else { "${busy}.0%" }
	$load = if ($null -eq $queue) { '-' } else { "$queue" }
	Write-Host "resource $now root $root MB, workspace $work MB, memory $free MB, cpu busy $cpu, queue $load"
	Start-Sleep -Seconds 30
}
