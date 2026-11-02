#!/bin/sh
# Starts an APE through this host's loader many times, so the dats duration of each case is its launch cost. reexec is a normal start.
set -u

mode=${1:?usage: launch-time.sh reexec|once <ape> <runs>}
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

# perl sets argv[0] apart from the file it execs. Both modes go through it,
# so its own cost is in both durations.
case $mode in
reexec) set -- "$loader" "$ape" 10 5 ;;
once) set -- "$ape" 10 5; APE_REEXEC=; export APE_REEXEC ;;
*) echo "unknown mode $mode" >&2; exit 1 ;;
esac

count=0
while [ "$count" -lt "$runs" ]; do
	out=$(perl -e '$file = shift; exec {$file} @ARGV or die "exec $file: $!\n"' "$loader" "$@") || exit 1
	[ "$out" = fizzbuzz ] || { echo "run $count printed '$out', not fizzbuzz" >&2; exit 1; }
	count=$((count + 1))
done
echo "$runs $mode launches: fizzbuzz"
