#!/bin/sh
# Report free disk, free memory, CPU utilisation and load every half minute,
# into the suite's own stream. A runner that dies mid-job leaves no step after
# it to ask, so the last line printed is the only account of what the machine
# had left, and a suite that takes forty minutes leaves no account at all of
# where the time went unless something samples the processor while it runs.
#
# CPU is a percentage of the interval that just ended, from /proc/stat deltas,
# never a total since boot. The first line has no interval behind it and
# reports a dash for each share. The core count is printed once at the start,
# because a load average means nothing without it.
#
# A probe, not a check: it asserts nothing and never fails the build.
#
# SIGTERM ends it at once, its sleep included. Whoever reads the suite's
# output waits for every process holding that pipe, and an orphaned sleep
# is one of them.

nap=
trap 'if [ -n "$nap" ]; then kill "$nap"; fi; exit 0' TERM

# readcpu sets busy, idle and total from the aggregate line of /proc/stat.
# The guest columns are left out: the kernel already counts guest time in
# user, so adding them again would make the shares sum to more than the
# interval.
readcpu() {
	read -r label cuser cnice csys cidle ciow cirq csirq csteal rest < /proc/stat
	: "${cuser:=0}" "${cnice:=0}" "${csys:=0}" "${cidle:=0}"
	: "${ciow:=0}" "${cirq:=0}" "${csirq:=0}" "${csteal:=0}"
	nowuser=$((cuser + cnice))
	nowsys=$((csys + cirq + csirq + csteal))
	nowiow=$ciow
	nowidle=$cidle
	nowtotal=$((nowuser + nowsys + nowiow + nowidle))
}

# share prints one column's part of the interval, to a tenth of a percent.
share() {
	if [ "$2" -le 0 ]; then
		printf -- '-'
		return
	fi
	tenths=$((1000 * $1 / $2))
	printf '%d.%d%%' $((tenths / 10)) $((tenths % 10))
}

cores=$(grep -c '^cpu[0-9]' /proc/stat)
echo "resource cores ${cores}"

readcpu
lastuser=$nowuser
lastsys=$nowsys
lastiow=$nowiow
lastidle=$nowidle
lasttotal=$nowtotal

while true; do
	now=$(date -u +%H:%M:%S)
	root=$(df -Pm / | tail -n 1 | tr -s ' ' | cut -d ' ' -f 4)
	work=$(df -Pm "${GITHUB_WORKSPACE:-.}" | tail -n 1 | tr -s ' ' | cut -d ' ' -f 4)
	free=$(grep MemAvailable /proc/meminfo | tr -s ' ' | cut -d ' ' -f 2)
	load=$(cut -d ' ' -f 1-3 /proc/loadavg)
	readcpu
	span=$((nowtotal - lasttotal))
	echo "resource ${now} root ${root} MB, workspace ${work} MB, memory $((free / 1024)) MB, cpu user $(share $((nowuser - lastuser)) "$span") sys $(share $((nowsys - lastsys)) "$span") iowait $(share $((nowiow - lastiow)) "$span") idle $(share $((nowidle - lastidle)) "$span"), load ${load}"
	lastuser=$nowuser
	lastsys=$nowsys
	lastiow=$nowiow
	lastidle=$nowidle
	lasttotal=$nowtotal
	# In the background, so a TERM interrupts the wait rather than queueing behind the sleep.
	sleep 30 &
	nap=$!
	wait "$nap"
done
