#!/usr/bin/env bash
# bubble-serial.sh -- a test that opens a synctest bubble must take the serial
# barrier BEFORE it, never from inside.
#
# T.Serial stops every other test in the process, so it waits on a condition
# only another test can clear. A goroutine inside a synctest bubble that waits
# on it parks alongside every other goroutine in that bubble, and synctest
# reports "deadlock: all goroutines in bubble are blocked" naming neither
# Serial nor the helper that asked for it. Serial is idempotent, so hoisting
# the ask into the wrapper costs the caller nothing.

set -uo pipefail

root=${1:-.}
fail=0

# Per directory: which non-test funcs reach Serial(), directly or through
# another one, and which bubble bodies call one of those.
while IFS= read -r dir; do
	files=$(ls "$dir"/*_test.go 2>/dev/null) || continue
	[ -n "$files" ] || continue
	grep -lq 'synctest\.Test(' $files 2>/dev/null || continue

	helpers=$(awk '
		/^func [A-Za-z_]/ { name = $2; sub(/[[(].*/, "", name) }
		/Serial\(\)/      { if (name != "" && name !~ /^Test/) print name }
	' $files | sort -u)
	[ -n "$helpers" ] || continue

	for _ in 1 2 3; do
		re=$(printf '%s|' $helpers | sed 's/|$//')
		more=$(awk -v re="$re" '
			/^func [A-Za-z_]/ { name = $2; sub(/[[(].*/, "", name) }
			$0 ~ "(" re ")\\(" { if (name != "" && name !~ /^Test/) print name }
		' $files | sort -u)
		helpers=$(printf '%s\n%s\n' "$helpers" "$more" | sort -u)
	done
	re=$(printf '%s|' $helpers | sed 's/|$//')

	# Each wrapper, the bubble body it runs, and whether it took the barrier.
	while IFS=$'\t' read -r wrapper body; do
		[ -n "$body" ] || continue
		bodysrc=$(awk -v f="func $body(" 'index($0,f)==1{p=1} p{print} p&&/^}$/{exit}' $files)
		printf '%s' "$bodysrc" | grep -qE "($re)\(" || continue

		wrapsrc=$(awk -v f="func $wrapper(" 'index($0,f)==1{p=1} p{print} p&&/^}$/{exit}' $files)
		if ! printf '%s' "$wrapsrc" | grep -q '\.Serial()'; then
			printf 'BLOCKED: %s opens a bubble that waits on the serial barrier\n' "$wrapper" >&2
			printf '  %s reaches Serial() but %s never takes it first\n' "$body" "$wrapper" >&2
			fail=1
		fi
	done < <(grep -hoE 'func (Test[A-Za-z0-9_]+)\(t \*testing\.T\) \{ synctest\.Test\(t, (test[A-Za-z0-9_]+)\)|synctest\.Test\(t, (test[A-Za-z0-9_]+)\)' $files >/dev/null 2>&1
		awk '
			/^func Test[A-Za-z0-9_]*\(t \*testing\.T\)/ { w = $2; sub(/\(.*/, "", w) }
			match($0, /synctest\.Test\(t, (test[A-Za-z0-9_]+)\)/) {
				b = substr($0, RSTART, RLENGTH)
				sub(/.*, /, "", b); sub(/\)/, "", b)
				if (w != "") print w "\t" b
			}
		' $files)
done < <(find "$root/src" -type d 2>/dev/null)

if [ "$fail" -ne 0 ]; then
	printf '\nTake the barrier in the test that calls synctest.Test, above it.\n' >&2
	printf 'Serial is idempotent, so the ask inside the bubble still runs.\n' >&2
	exit 2
fi
exit 0
