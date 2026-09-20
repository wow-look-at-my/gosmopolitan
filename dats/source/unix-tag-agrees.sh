#!/usr/bin/env bash
# unix-tag-agrees.sh -- cmd/dist and internal/syslist must name the same
# GOOS set for the "unix" build tag.
#
# dist cannot import internal/syslist: it compiles against the BOOTSTRAP
# toolchain's std, so the list is copied. A copy that drifts makes the two
# builders disagree about which files a package holds.

set -uo pipefail

root=${1:-.}
dist="$root/src/cmd/dist/build.go"
syslist="$root/src/internal/syslist/syslist.go"

# firstKey prints the first quoted lowercase-alnum token on a line, and
# returns 1 when the line carries none.
firstKey() {
	rest=$1
	while [ "$rest" != "${rest#*\"}" ]; do
		rest=${rest#*\"}
		word=${rest%%\"*}
		[ "$word" != "$rest" ] || return 1
		rest=${rest#*\"}
		case $word in
		"" | *[!a-z0-9]*) continue ;;
		esac
		printf '%s\n' "$word"
		return 0
	done
	return 1
}

# Print the quoted keys of the named map literal, one per line.
keys() {
	inmap=0
	while IFS= read -r text || [ -n "$text" ]; do
		if [ "$inmap" -eq 0 ]; then
			case $text in
			"var $2 = map[string]bool{"*) inmap=1 ;;
			esac
			continue
		fi
		case $text in
		"}"*) break ;;
		esac
		firstKey "$text" || true
	done <"$1" | sort
}

a=$(keys "$dist" unixOS)
b=$(keys "$syslist" UnixOS)

for pair in "unixOS:$a" "UnixOS:$b"; do
	if [ -z "${pair#*:}" ]; then
		printf 'BLOCKED: found no %s map to read\n' "${pair%%:*}" >&2
		exit 2
	fi
done

if [ "$a" = "$b" ]; then
	exit 0
fi

printf 'BLOCKED: the two "unix" tag lists disagree\n' >&2
diff <(printf '%s\n' "$a") <(printf '%s\n' "$b") |
	while IFS= read -r text; do
		case $text in
		"<"*) printf '  only in cmd/dist:     %s\n' "${text#<}" ;;
		">"*) printf '  only in syslist:      %s\n' "${text#>}" ;;
		*) printf '%s\n' "$text" ;;
		esac
	done >&2
printf '\nA GOOS in one list and not the other means dist and cmd/go build\n' >&2
printf 'different files for it. Add it to both.\n' >&2
exit 2
