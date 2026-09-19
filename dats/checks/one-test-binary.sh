#!/usr/bin/env bash
# one-test-binary.sh GOOS GOARCH PATTERN... -- refuse a go test run over
# several packages that links more than one test binary.
#
# Every package's tests go into ONE binary per port, run with -test.unit per
# package. go test -n prints the plan without running it. -c -o /dev/null
# keeps a cached test result from sparing its binary the link.
#
# The packages and flags are dist test's short mode ones: the patterns less
# vendored code, and -pgo=off, because a binary holds one PGO profile.

set -uo pipefail

if [ "$#" -lt 3 ]; then
	echo "usage: ${0##*/} GOOS GOARCH PATTERN..." >&2
	exit 2
fi
goos=$1
goarch=$2
shift 2

listed=$(GOOS=$goos GOARCH=$goarch go list "$@") || exit 2
pkgs=()
while IFS= read -r pkg; do
	case $pkg in
	vendor/* | cmd/vendor/*) ;;
	*) pkgs+=("$pkg") ;;
	esac
done <<<"$listed"

plan=$(GOOS=$goos GOARCH=$goarch go test -c -o /dev/null -pgo=off -n "${pkgs[@]}" 2>&1)
status=$?
if [ "$status" -ne 0 ]; then
	printf '%s\n' "$plan" >&2
	echo "go test -n $* for $goos/$goarch failed" >&2
	exit 2
fi

links=$(printf '%s\n' "$plan" | grep -c -E '(/link| tool link) -o ')
if [ "$links" -ne 1 ]; then
	printf '%s\n' "$plan" | grep -E '(/link| tool link) -o ' >&2
	echo "go test $* for $goos/$goarch links $links test binaries, want 1" >&2
	exit 1
fi
exit 0
