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

# Print the quoted keys of the named map literal, one per line.
keys() {
	awk -v want="$2" '
		$0 ~ "^var " want " = map\\[string\\]bool\\{" { in_map = 1; next }
		in_map && /^\}/                               { exit }
		in_map && match($0, /"[a-z0-9]+"/) {
			print substr($0, RSTART + 1, RLENGTH - 2)
		}
	' "$1" | sort
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
	sed 's/^</  only in cmd\/dist:     /; s/^>/  only in syslist:      /' >&2
printf '\nA GOOS in one list and not the other means dist and cmd/go build\n' >&2
printf 'different files for it. Add it to both.\n' >&2
exit 2
