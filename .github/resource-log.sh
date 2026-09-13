#!/bin/sh
# Report free disk and free memory every half minute, into the suite's own
# stream. A runner that dies mid-job leaves no step after it to ask, so the
# last line printed is the only account of what the machine had left.
#
# A probe, not a check: it asserts nothing and never fails the build.

while true; do
	now=$(date -u +%H:%M:%S)
	root=$(df -Pm / | tail -n 1 | tr -s ' ' | cut -d ' ' -f 4)
	work=$(df -Pm "${GITHUB_WORKSPACE:-.}" | tail -n 1 | tr -s ' ' | cut -d ' ' -f 4)
	free=$(grep MemAvailable /proc/meminfo | tr -s ' ' | cut -d ' ' -f 2)
	echo "resource ${now} root ${root} MB, workspace ${work} MB, memory $((free / 1024)) MB"
	sleep 30
done
