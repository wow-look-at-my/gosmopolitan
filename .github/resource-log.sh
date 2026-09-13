#!/bin/sh
# Report free disk and free memory every half minute, into the suite's own
# stream. A runner that dies mid-job leaves no step after it to ask, so the
# last line printed is the only account of what the machine had left.
#
# A probe, not a check: it asserts nothing and never fails the build.
#
# SIGTERM ends it at once, its sleep included. Whoever reads the suite's
# output waits for every process holding that pipe, and an orphaned sleep
# is one of them.

nap=
trap 'if [ -n "$nap" ]; then kill "$nap"; fi; exit 0' TERM

while true; do
	now=$(date -u +%H:%M:%S)
	root=$(df -Pm / | tail -n 1 | tr -s ' ' | cut -d ' ' -f 4)
	work=$(df -Pm "${GITHUB_WORKSPACE:-.}" | tail -n 1 | tr -s ' ' | cut -d ' ' -f 4)
	free=$(grep MemAvailable /proc/meminfo | tr -s ' ' | cut -d ' ' -f 2)
	echo "resource ${now} root ${root} MB, workspace ${work} MB, memory $((free / 1024)) MB"
	# In the background, so a TERM interrupts the wait rather than queueing behind the sleep.
	sleep 30 &
	nap=$!
	wait "$nap"
done
