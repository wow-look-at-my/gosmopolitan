#!/bin/sh
# A continue-on-error hides a red leg, and a constant-false `if` hides a whole job. Either needs a dated marker.
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

# waiverDate prints the first dated waiver-expires marker on a line.
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
