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
# The cache lives only under a sticky directory, as /tmp is.
chmod 1700 "$cache" || exit 1
case $mode in
installed) APE_LOADER=$loader ;;
cached | cold) APE_LOADER= ;;
unpack)
	# The boot before the cache unpacked the loader on every launch, then test-ran it.
	APE_LOADER=
	gz=
	[ "$os" = darwin ] && gz=yes
	line=$(dd if="$ape" bs=512 count=128 2>/dev/null | grep -a -m1 "skip=.*count=.*${gz:+gzip -dc}")
	skip=${line#*skip=}; skip=${skip%% *}
	num=${line#*count=}; num=${num%% *}
	;;
*) echo "unknown mode $mode" >&2; exit 1 ;;
esac
APE_LOADERDIR=$cache
export APE_LOADER APE_LOADERDIR

boot() {
	case $mode in
	cold) rm -rf "$cache"/.ape-* && sh "$ape" 10 5 ;;
	unpack)
		u=$cache/.unpack.$$
		uname -m >/dev/null
		if [ -n "$gz" ]; then
			dd if="$ape" bs=512 skip="$skip" count="$num" 2>/dev/null | gzip -dc >"$u"
		else
			dd if="$ape" bs=512 skip="$skip" count="$num" 2>/dev/null >"$u"
		fi
		chmod 700 "$u" && { "$u" >/dev/null 2>&1; "$u" "$ape" 10 5; }
		rm -f "$u"
		;;
	*) sh "$ape" 10 5 ;;
	esac
}

count=0
while [ "$count" -lt "$runs" ]; do
	out=$(boot) || exit 1
	[ "$out" = fizzbuzz ] || { echo "run $count printed '$out', not fizzbuzz" >&2; exit 1; }
	count=$((count + 1))
done
echo "$runs $mode launches: fizzbuzz"
