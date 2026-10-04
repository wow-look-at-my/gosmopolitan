#!/usr/bin/env bash
# profile-suite.bash OUTDIR -- runs src/run.bash with process accounting and system-wide perf sampling on, so the run says which programs.

set -euo pipefail

if [ "$#" -ne 1 ]; then
	echo "usage: ${0##*/} OUTDIR" >&2
	exit 2
fi
out=$(mkdir -p "$1" && cd "$1" && pwd)
root=$(cd "$(dirname "$0")/../.." && pwd)

for tool in accton perf; do
	if ! command -v "$tool" >/dev/null; then
		echo "${0##*/}: $tool is not on PATH" >&2
		exit 1
	fi
done
if [ ! -x "$root/bin/go" ]; then
	echo "${0##*/}: $root/bin/go is missing; run src/make.bash first" >&2
	exit 1
fi

: >"$out/pacct"
accton "$out/pacct"
perf record -a -g -F 19 -o "$out/perf.data" >"$out/perf-record.log" 2>&1 &
perfpid=$!
stop() {
	kill -INT "$perfpid" 2>/dev/null || true
	wait "$perfpid" || true
	accton off
}
trap stop EXIT

start=$SECONDS
rc=0
(cd "$root/src" && ./run.bash) >"$out/suite.log" 2>&1 || rc=$?
echo "suite exit $rc in $((SECONDS - start))s; profile in $out"
exit "$rc"
