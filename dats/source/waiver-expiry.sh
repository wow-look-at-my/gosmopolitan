#!/bin/sh
# A continue-on-error hides a red leg, and a constant-false `if` hides a
# whole job. Either needs a dated marker, on the line above the key or on
# the key's own line, and the date has to be in the future:
#
#	# ... waiver-expires: 2026-09-18
#	continue-on-error: ${{ matrix.suite-os == 'windows-latest' }}
#	if: ${{ false }} # waiver-expires: 2026-09-21
#
# Usage: waiver-expiry.sh <file>...
# Exit: 0 every waiver is dated and current, 2 one is not.
set -eu

today=${WAIVER_TODAY:-$(date -u +%Y-%m-%d)}
rc=0
tab=$(printf '\t')

# skipBlanks strips leading spaces and tabs, leaving the rest in `trimmed`.
skipBlanks() {
	trimmed=$1
	while :; do
		case $trimmed in
		" "*) trimmed=${trimmed# } ;;
		"$tab"*) trimmed=${trimmed#"$tab"} ;;
		*) return 0 ;;
		esac
	done
}

# waiverDate prints the first dated waiver-expires marker on a line. It
# returns 1 when no marker carries a well-formed date.
waiverDate() {
	rest=$1
	while [ "$rest" != "${rest#*waiver-expires:}" ]; do
		rest=${rest#*waiver-expires:}
		skipBlanks "$rest"
		when=${trimmed%"${trimmed#??????????}"}
		case $when in
		[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9])
			printf '%s' "$when"
			return 0
			;;
		esac
	done
	return 1
}

# dateNum turns YYYY-MM-DD into one comparable number.
dateNum() {
	rest=${1#*-}
	printf '%s%s%s' "${1%%-*}" "${rest%%-*}" "${rest#*-}"
}

for f in "$@"; do
	[ -f "$f" ] || continue
	out=$(
		num=0
		prev=""
		while IFS= read -r text || [ -n "$text" ]; do
			num=$((num + 1))
			kind=""
			case $text in
			*continue-on-error*) kind="continue-on-error" ;;
			*)
				skipBlanks "$text"
				case $trimmed in
				"if: false" | "if: false "* | "if: \${{ false }}" | "if: \${{ false }}"*)
					kind="a job switched off with if: false"
					;;
				esac
				;;
			esac
			if [ -n "$kind" ]; then
				if when=$(waiverDate "$prev $text"); then
					if [ "$(dateNum "$when")" -lt "$(dateNum "$today")" ]; then
						printf '%s:%d: waiver expired on %s (today is %s)\n' "$f" "$num" "$when" "$today"
					fi
				else
					printf '%s:%d: %s with no waiver-expires date\n' "$f" "$num" "$kind"
				fi
			fi
			prev=$text
		done <"$f"
	)
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
