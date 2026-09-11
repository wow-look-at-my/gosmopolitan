#!/bin/sh
# A continue-on-error in a workflow hides a red leg. This refuses one that
# carries no expiry date, and refuses one whose date has passed, so a
# waiver removes itself instead of outliving the reason it was granted.
#
# The marker sits on the line above the key, or on the key's own line:
#
#	# ... waiver-expires: 2026-09-18
#	continue-on-error: ${{ matrix.suite-os == 'windows-latest' }}
#
# Usage: waiver-expiry.sh <file>...
# Exit: 0 every waiver is dated and current, 2 one is not.
set -eu

today=${WAIVER_TODAY:-$(date -u +%Y-%m-%d)}
rc=0

for f in "$@"; do
	[ -f "$f" ] || continue
	# awk carries the previous line, so a marker above the key counts.
	out=$(awk -v file="$f" -v today="$today" '
		/continue-on-error/ {
			line = prev " " $0
			if (match(line, /waiver-expires:[ \t]*[0-9]{4}-[0-9]{2}-[0-9]{2}/)) {
				d = substr(line, RSTART, RLENGTH)
				sub(/.*waiver-expires:[ \t]*/, "", d)
				if (d < today)
					printf "%s:%d: waiver expired on %s (today is %s)\n", file, NR, d, today
			} else {
				printf "%s:%d: continue-on-error with no waiver-expires date\n", file, NR
			}
		}
		{ prev = $0 }
	' "$f")
	if [ -n "$out" ]; then
		printf '%s\n' "$out" >&2
		rc=2
	fi
done

if [ "$rc" -ne 0 ]; then
	cat >&2 <<'EOF'

A continue-on-error keeps a failing leg out of the branch's way. Either
fix what it covers and delete the key, or move the date out after saying
so to whoever granted it.
EOF
fi
exit "$rc"
