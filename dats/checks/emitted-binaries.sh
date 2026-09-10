#!/usr/bin/env bash
# emitted-binaries.sh -- report the container format of each file named.
# It prints "ape", "wasm" or "OTHER <first bytes>" per file, and exits
# non-zero if any file is neither. The APE magic is the MZqFpD header a
# .com carries; wasm's is the four bytes \0asm.

set -uo pipefail

fail=0
for f in "$@"; do
	if [ ! -f "$f" ]; then
		printf 'MISSING %s\n' "$f" >&2
		fail=1
		continue
	fi
	head8=$(dd if="$f" bs=1 count=8 2>/dev/null | od -An -tx1 | tr -d ' \n')
	case "$head8" in
	4d5a714670443d27*) printf 'ape  %s\n' "$f" ;;
	0061736d*) printf 'wasm %s\n' "$f" ;;
	*)
		printf 'OTHER %s (starts %s)\n' "$f" "$head8" >&2
		fail=1
		;;
	esac
done
exit $fail
