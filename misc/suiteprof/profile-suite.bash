#!/usr/bin/env bash
# profile-suite.bash OUTDIR -- runs src/run.bash under process accounting and perf.
# It writes OUTDIR/report.md: the CPU of each program, and the hottest functions.
# Linux only. A non-root caller needs sudo, for accton and perf alone.
# The suite runs as the caller, because some tests behave differently as root.

set -euo pipefail

if [ "$#" -ne 1 ]; then
	echo "usage: ${0##*/} OUTDIR" >&2
	exit 2
fi
out=$(mkdir -p "$1" && cd "$1" && pwd)
root=$(cd "$(dirname "$0")/../.." && pwd)

asroot=()
if [ "$(id -u)" -ne 0 ]; then
	asroot=(sudo)
fi
for tool in accton sa perf; do
	if ! command -v "$tool" >/dev/null; then
		echo "${0##*/}: $tool is not on PATH" >&2
		exit 1
	fi
done
if [ ! -x "$root/bin/go" ]; then
	echo "${0##*/}: $root/bin/go is missing; run src/make.bash first" >&2
	exit 1
fi

"${asroot[@]}" rm -f "$out/pacct"
"${asroot[@]}" touch "$out/pacct"
"${asroot[@]}" accton "$out/pacct"
# perf closes a data file every interval, so each one is a finished slice of
# the run that can be reported while the suite goes on.
interval=300
"${asroot[@]}" perf record -a -g -F 19 --switch-output="${interval}s" -o "$out/perf.data" >"$out/perf-record.log" 2>&1 &
perfpid=$!

# programs prints the CPU each program used so far, from process accounting.
programs() {
	echo '```'
	"${asroot[@]}" sa -a "$out/pacct" | head -"$1"
	echo '```'
}

# hottest prints the most sampled functions in the perf files named.
hottest() {
	local limit=$1
	shift
	echo '```'
	for data in "$@"; do
		"${asroot[@]}" perf report -i "$data" --stdio --no-children -g none \
			--sort comm,sym --percent-limit 0.5 2>/dev/null | grep -v -e '^#' -e '^$' | head -"$limit"
	done
	echo '```'
}

# Every interval a snapshot goes to the report and the log, so a run that is
# cancelled or times out still leaves what it measured.
: >"$out/report.md"
start=$SECONDS
(
	reported=""
	while sleep "$interval"; do
		latest=""
		for data in "$out"/perf.data.*; do
			[ -e "$data" ] && latest=$data
		done
		{
			echo "### Suite profile at $((SECONDS - start))s"
			echo
			echo "#### CPU by program so far"
			programs 25
			if [ -n "$latest" ] && [ "$latest" != "$reported" ]; then
				echo "#### Hottest functions in ${latest##*/}"
				hottest 25 "$latest"
				reported=$latest
			fi
		} | tee -a "$out/report.md"
	done
) &
snapper=$!

stopped=0
stop() {
	[ "$stopped" -eq 1 ] && return
	stopped=1
	kill "$snapper" 2>/dev/null || true
	"${asroot[@]}" kill -INT "$perfpid" 2>/dev/null || true
	wait "$perfpid" || true
	"${asroot[@]}" accton off
}
trap stop EXIT

rc=0
(cd "$root/src" && ./run.bash) 2>&1 | tee "$out/suite.log" || rc=$?
wall=$((SECONDS - start))
stop

{
	echo "### Suite profile: the whole run"
	echo
	echo "run.bash exited $rc after ${wall}s."
	echo
	echo "#### CPU by program"
	programs 40
	echo "#### Hottest functions, slice by slice"
	hottest 15 "$out"/perf.data*
} | tee -a "$out/report.md"
exit "$rc"
