#!/usr/bin/env bash
# goos-not-syscall-flags.sh -- refuse a runtime.GOOS predicate that decides an
# open flag.
#
# runtime.GOOS names the HOST here, so a cosmo binary on NT takes every
# `== "windows"` branch while its syscall layer stays POSIX-shaped. For
# filesystem semantics the host is right. For a syscall flag the build target
# is, and the branch must read internal/goos.IsWindows, which is 0 for cosmo.
#
# os/removeall_at.go shipped the second kind: O_WRONLY|O_RDWR on a directory,
# which is EISDIR under cosmo, failing every t.TempDir cleanup.

set -uo pipefail

root=${1:-.}
fail=0

# An open flag named within 6 lines after a GOOS comparison. That window is the
# whole predicate body in the shipped bug and in every shape like it.
pat='runtime\.GOOS[[:space:]]*[=!]=[[:space:]]*"'
flags='O_(RDONLY|WRONLY|RDWR|CREATE|CREAT|APPEND|EXCL|SYNC|TRUNC|NOFOLLOW|DIRECTORY|CLOEXEC)'

# cosmoBuilds reports whether a cosmo build compiles this file at all. A
# constraint that names an explicit GOOS list and leaves cosmo out of it takes
# the file out of scope: flagging a file cosmo never compiles teaches a reader
# to skim this guard's output, and then the one real hit goes past too.
cosmoBuilds() {
	local line
	line=$(grep -m1 '^//go:build ' "$1" 2>/dev/null) || return 0
	[ -n "$line" ] || return 0
	case "$line" in
	*cosmo* | *unix*) return 0 ;;
	esac
	# Any other named GOOS means the list is explicit and cosmo is absent.
	printf '%s' "$line" | grep -qE \
		'\b(aix|android|darwin|dragonfly|freebsd|hurd|illumos|ios|js|linux|netbsd|openbsd|plan9|solaris|wasip1|windows|zos)\b' &&
		return 1
	return 0
}

while IFS= read -r hit; do
	file=${hit%%:*}
	rest=${hit#*:}
	line=${rest%%:*}
	cosmoBuilds "$file" || continue
	window=$(awk -v s="$line" -v e="$((line + 6))" \
		'NR >= s && NR <= e' "$file" 2>/dev/null)
	if printf '%s' "$window" | grep -qE "$flags"; then
		printf 'BLOCKED: runtime.GOOS decides an open flag\n  %s:%s\n' "$file" "$line" >&2
		fail=1
	fi
done < <(grep -rnE "$pat" --include='*.go' "$root/src/os" "$root/src/internal" \
	"$root/src/syscall" "$root/src/path" 2>/dev/null |
	grep -v '_test\.go:' || true)

if [ "$fail" -ne 0 ]; then
	printf '\nruntime.GOOS is the HOST here, and a syscall flag belongs to the\n' >&2
	printf 'BUILD TARGET. Read internal/goos.IsWindows instead.\n' >&2
	exit 2
fi
exit 0
