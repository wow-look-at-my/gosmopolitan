#!/usr/bin/env bash
# goos-readonly.sh -- refuse any assignment to runtime.GOOS or
# runtime.GOARCH.
#
# Go has no read-only exported variable, and on cosmo these two must be
# variables: one APE boots on several kernels. So the compiler cannot
# stop a package from writing to them, and this check does.
#
# Only setGOOS and setGOARCH in runtime/goos_cosmo.go may assign.
#
# It lives here, in the repo, because dats/goos-readonly.dats runs it in
# CI: a check that runs a file the checkout does not contain enforces
# nothing.

set -uo pipefail

root=${1:-.}
fail=0

# An assignment, not a comparison: "= x" but never "== x", "!= x", ">= x".
pat='runtime\.(GOOS|GOARCH)[[:space:]]*(=[^=]|\+=|:=)'

while IFS= read -r hit; do
	printf 'BLOCKED: assignment to runtime.GOOS/GOARCH\n  %s\n' "$hit" >&2
	fail=1
done < <(grep -rnE "$pat" --include='*.go' "$root/src" 2>/dev/null |
	grep -v '_test\.go:' || true)

# Inside package runtime the selector is absent, so match the bare name.
# The space before "=" is what separates a Go assignment, which gofmt
# always spaces, from a shell env prefix in a comment: the file headers
# are full of "GOARCH=386 go tool cgo ...".
bare='^[[:space:]]*(GOOS|GOARCH) (=[^=]|\+=)'
while IFS= read -r hit; do
	case "$hit" in
	*/runtime/goos_cosmo.go:*) continue ;;
	esac
	printf 'BLOCKED: assignment to GOOS/GOARCH inside package runtime\n  %s\n' "$hit" >&2
	fail=1
done < <(grep -rnE "$bare" --include='*.go' "$root/src/runtime" 2>/dev/null |
	grep -v '_test\.go:' || true)

if [ "$fail" -ne 0 ]; then
	printf '\nGOOS and GOARCH report the host. A package that writes to them\n' >&2
	printf 'makes every other package lie. Read them; do not set them.\n' >&2
	exit 2
fi
exit 0
