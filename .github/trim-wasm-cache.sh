#!/bin/sh
# Trim the restored wasmtime module cache down to CAP, oldest entry first.
#
# The workflow restores an earlier copy of this directory and then saves a
# bigger one, so it ratchets up run over run. One measured wasip1 suite adds
# about 3.8 GB.
#
# wasmtime's own soft limit does not bound this. Its cleanup deletes by age,
# and inside one suite every entry is recent, so a run that measured 3.8 GB
# stayed at 3.8 GB with cleanup running every two minutes.
#
# Evict, never drop the directory: deleting it costs the run every module
# compile the cache exists to avoid, and the oversized copy it then saves
# trips the cap again on the next run. Oldest-first keeps what a suite is
# most likely to reuse.

set -eu

CAP_MB="${CAP_MB:-2048}"
DIR="${WASMTIME_CACHE_DIR:-$HOME/.cache/wasmtime}"

if [ ! -d "$DIR" ]; then
	echo "wasmtime cache: $DIR is not there yet, nothing to trim"
	exit 0
fi

sizeMB() { du -sm "$DIR" | cut -f 1; }

have=$(sizeMB)
if [ "$have" -le "$CAP_MB" ]; then
	echo "wasmtime cache: ${have} MB, at or under the ${CAP_MB} MB cap, kept"
	exit 0
fi

echo "wasmtime cache: ${have} MB is over the ${CAP_MB} MB cap, evicting oldest first"

# Each line is "<mtime> <bytes> <path>", oldest first. The two numeric keys
# sit ahead of the path, so a name holding a space cannot shift them.
listing=$(mktemp)
find "$DIR" -type f -printf '%T@ %s %p\n' | sort -n > "$listing"

# Free by byte accounting rather than re-running du per file: stop the moment
# enough has gone, so the entries a suite will reuse stay.
freeBytes=$(( (have - CAP_MB) * 1024 * 1024 ))
freed=0
removed=0
while IFS=' ' read -r _stamp bytes path; do
	[ "$freed" -lt "$freeBytes" ] || break
	[ -n "$path" ] || continue
	rm -f "$path" 2>/dev/null || continue
	freed=$((freed + bytes))
	removed=$((removed + 1))
done < "$listing"

rm -f "$listing"
# -mindepth 1 so the cache directory itself survives an eviction that empties it.
find "$DIR" -mindepth 1 -type d -empty -delete 2>/dev/null || true

left=$(sizeMB)
echo "wasmtime cache: evicted ${removed} entries, ${have} MB -> ${left} MB"

if [ "$left" -gt "$CAP_MB" ]; then
	echo "wasmtime cache: still ${left} MB after evicting every file it could" >&2
	exit 1
fi
