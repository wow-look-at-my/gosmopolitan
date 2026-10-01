#!/bin/sh
# Report free disk, free memory, CPU utilisation and load every half minute.
# A runner that dies mid-job leaves no step after it to ask, so the last line
# printed is the only account of what the machine had left.
#
# On a /proc host CPU is a share of the interval that just ended, from
# /proc/stat deltas. The first line has no interval behind it and reports a
# dash. The core count is printed once: a load average means nothing without
# it.
#
# A probe, not a check: it asserts nothing and never fails the build.
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

# A darwin host serves the same columns from top and vm_stat. top reports the
# share of the interval it just sampled, so no delta arithmetic applies there.
if [ -r /proc/stat ]; then
	kind=proc
	cores=$(grep -c '^cpu[0-9]' /proc/stat)
else
	kind=bsd
	cores=$(sysctl -n hw.ncpu)
fi
echo "resource cores ${cores}"

if [ "$kind" = bsd ]; then
	while true; do
		now=$(date -u +%H:%M:%S)
		root=$(df -Pm / | tail -n 1 | tr -s ' ' | cut -d ' ' -f 4)
		work=$(df -Pm "${GITHUB_WORKSPACE:-.}" | tail -n 1 | tr -s ' ' | cut -d ' ' -f 4)
		pagesize=$(sysctl -n hw.pagesize)
		freepages=$(vm_stat | grep 'Pages free' | tr -d '.' | tr -s ' ' | cut -d ' ' -f 3)
		spec=$(vm_stat | grep 'Pages speculative' | tr -d '.' | tr -s ' ' | cut -d ' ' -f 3)
		free=$(((freepages + spec) * pagesize / 1048576))
		load=$(sysctl -n vm.loadavg | tr -d '{}' | tr -s ' ' | cut -d ' ' -f 2-4)
		# The run queue over the core count, which sysctl answers under any
		# sandbox. top and ps are both refused under a seatbelt profile.
		one=$(echo "$load" | cut -d ' ' -f 1)
		busy=$(echo "$one $cores" | awk '{ printf "%d", $1 * 1000 / $2 }')
		echo "resource ${now} root ${root} MB, workspace ${work} MB, memory ${free} MB, cpu busy $((busy / 10)).$((busy % 10))%, load ${load}"
		sleep 30 &
		nap=$!
		wait "$nap"
	done
fi

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
