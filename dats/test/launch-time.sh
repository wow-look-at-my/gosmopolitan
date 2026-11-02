#!/bin/sh
# Starts an APE through its boot script many times.
set -u

mode=${1:?usage: launch-time.sh installed|cached <ape> <runs>}
ape=${2:?usage: launch-time.sh <mode> <ape> <runs>}
runs=${3:?usage: launch-time.sh <mode> <ape> <runs>}

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "no loader for $(uname -s)" >&2; exit 1 ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "no loader for $(uname -m)" >&2; exit 1 ;;
esac
loader=$PWD/src/cmd/link/internal/ld/apeld/bin/apeld-$os-$arch
[ -x "$loader" ] || { echo "$loader is missing from the checkout" >&2; exit 1; }

cache=${TMPDIR:-/tmp}/launch-time-cache.$$
mkdir -p "$cache" || exit 1
trap 'rm -rf "$cache"' EXIT
case $mode in
installed) APE_LOADER=$loader ;;
cached) APE_LOADER= ;;
*) echo "unknown mode $mode" >&2; exit 1 ;;
esac
APE_LOADERDIR=$cache
export APE_LOADER APE_LOADERDIR

count=0
while [ "$count" -lt "$runs" ]; do
	out=$(sh "$ape" 10 5) || exit 1
	[ "$out" = fizzbuzz ] || { echo "run $count printed '$out', not fizzbuzz" >&2; exit 1; }
	count=$((count + 1))
done
echo "$runs $mode launches: fizzbuzz"
