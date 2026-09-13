#!/bin/sh
# Drop the restored wasmtime module cache when it is already too big.
#
# The workflow restores an earlier copy of this directory and then saves a
# bigger one, so it ratchets up run over run. A hosted runner has about 14 GB
# free. One measured wasip1 suite adds about 3.8 GB.
#
# wasmtime's own soft limit does not bound this. Its cleanup deletes by age,
# and inside one suite every entry is recent, so a run that measured 3.8 GB
# stayed at 3.8 GB with cleanup running every two minutes.
#
# Starting from at most CAP keeps the peak near CAP plus one suite.

set -eu

CAP_MB=2048
DIR="${WASMTIME_CACHE_DIR:-$HOME/.cache/wasmtime}"

if [ ! -d "$DIR" ]; then
	echo "wasmtime cache: $DIR is not there yet, nothing to trim"
	exit 0
fi

have=$(du -sm "$DIR" | cut -f 1)
if [ "$have" -le "$CAP_MB" ]; then
	echo "wasmtime cache: ${have} MB, at or under the ${CAP_MB} MB cap, kept"
	exit 0
fi

rm -rf "$DIR"
echo "wasmtime cache: ${have} MB was over the ${CAP_MB} MB cap, dropped"
