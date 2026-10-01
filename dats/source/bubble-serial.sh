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

# setFuncName puts the name a top-level `func` line declares into `fname`.
setFuncName() {
	case $1 in
	"func "[A-Za-z_]*) ;;
	*) return 0 ;;
	esac
	fname=${1#"func "}
	fname=${fname%%[[(]*}
}

# serialHelpers prints each non-test func whose body names Serial().
serialHelpers() {
	fname=""
	for src in "$@"; do
		while IFS= read -r text || [ -n "$text" ]; do
			setFuncName "$text"
			case $text in
			*"Serial()"*) ;;
			*) continue ;;
			esac
			case $fname in
			"" | Test*) continue ;;
			esac
			printf '%s\n' "$fname"
		done <"$src"
	done
}

# callsHelper reports whether its argument names a call to a known helper.
callsHelper() {
	for want in $helpers; do
		case $1 in
		*"$want("*) return 0 ;;
		esac
	done
	return 1
}

# helperCallers prints each non-test func that calls a known helper, which
# is how a helper's reach grows one hop per pass.
helperCallers() {
	fname=""
	for src in "$@"; do
		while IFS= read -r text || [ -n "$text" ]; do
			setFuncName "$text"
			callsHelper "$text" || continue
			case $fname in
			"" | Test*) continue ;;
			esac
			printf '%s\n' "$fname"
		done <"$src"
	done
}

# bubblePairs prints each Test func and the bubble body it hands to
# synctest.Test, separated by a tab.
bubblePairs() {
	wrap=""
	for src in "$@"; do
		while IFS= read -r text || [ -n "$text" ]; do
			case $text in
			"func Test"*"(t *testing.T)"*)
				wrap=${text#"func "}
				wrap=${wrap%%(*}
				;;
			esac
			case $text in
			*"synctest.Test(t, test"*) ;;
			*) continue ;;
			esac
			body=${text#*"synctest.Test(t, "}
			body=${body%%)*}
			case $body in
			test?*) ;;
			*) continue ;;
			esac
			case $body in
			*[!A-Za-z0-9_]*) continue ;;
			esac
			[ -n "$wrap" ] || continue
			printf '%s\t%s\n' "$wrap" "$body"
		done <"$src"
	done
}

# funcBody prints a top-level func's source, from its `func NAME(` line to
# the closing brace in column one.
funcBody() {
	open="func $1("
	shift
	found=0
	for src in "$@"; do
		while IFS= read -r text || [ -n "$text" ]; do
			case $text in
			"$open"*) found=1 ;;
			esac
			[ "$found" -eq 1 ] || continue
			printf '%s\n' "$text"
			[ "$text" = "}" ] || continue
			return 0
		done <"$src"
	done
}

# Per directory: which non-test funcs reach Serial(), directly or through
# another one, and which bubble bodies call one of those.
while IFS= read -r dir; do
	files=$(ls "$dir"/*_test.go 2>/dev/null) || continue
	[ -n "$files" ] || continue
	grep -lq 'synctest\.Test(' $files 2>/dev/null || continue

	helpers=$(serialHelpers $files | sort -u)
	[ -n "$helpers" ] || continue

	# Three hops of reach, which is as deep as a test helper chain goes.
	for pass in 1 2 3; do
		more=$(helperCallers $files | sort -u)
		helpers=$(printf '%s\n%s\n' "$helpers" "$more" | sort -u)
	done

	# Each wrapper, the bubble body it runs, and whether it took the barrier.
	while IFS=$'\t' read -r wrapper body; do
		[ -n "$body" ] || continue
		bodysrc=$(funcBody "$body" $files)
		callsHelper "$bodysrc" || continue

		wrapsrc=$(funcBody "$wrapper" $files)
		case $wrapsrc in
		*".Serial()"*) ;;
		*)
			printf 'BLOCKED: %s opens a bubble that waits on the serial barrier\n' "$wrapper" >&2
			printf '  %s reaches Serial() but %s never takes it first\n' "$body" "$wrapper" >&2
			fail=1
			;;
		esac
	done < <(bubblePairs $files)
done < <(find "$root/src" -type d 2>/dev/null)

if [ "$fail" -ne 0 ]; then
	printf '\nTake the barrier in the test that calls synctest.Test, above it.\n' >&2
	printf 'Serial is idempotent, so the ask inside the bubble still runs.\n' >&2
	exit 2
fi
exit 0
