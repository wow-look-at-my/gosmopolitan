#!/bin/sh
# Starts an APE through this host's loader many times, so the dats duration of the case is its launch cost.
set -u

ape=${1:?usage: launch-time.sh <ape> <runs>}
runs=${2:?usage: launch-time.sh <ape> <runs>}

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

count=0
while [ "$count" -lt "$runs" ]; do
	out=$("$loader" "$ape" 10 5) || exit 1
	[ "$out" = fizzbuzz ] || { echo "run $count printed '$out', not fizzbuzz" >&2; exit 1; }
	count=$((count + 1))
done
echo "$runs launches: fizzbuzz"
