#!/bin/sh
# Rebuilds the vendored APE loaders from their own sources and compares the
# result with the binaries this repo commits. Every APE carries those bytes
# and hands them to a host, so a binary nobody can reproduce is a binary
# nobody can audit.
#
# ZIG, LLD and STRIP name the tools. Run from the repo root. An argument
# names another apeld directory, which is how the negative case gets a
# corrupt tree to check without touching this one.
set -eu

ld=${1:-src/cmd/link/internal/ld/apeld}
[ -d "$ld/bin" ] || {
	echo "apeld-reproducible: no $ld/bin; run this from the repository root" >&2
	exit 2
}

missing=
for t in "${ZIG:-zig}" "${LLD:-ld64.lld}" "${STRIP:-llvm-strip}"; do
	command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
done
[ -z "$missing" ] || {
	echo "apeld-reproducible: not installed:$missing" >&2
	echo "  zig 0.16.0 builds the loaders; ld64.lld and llvm-strip come from llvm." >&2
	echo "  ZIG, LLD and STRIP name them if they are under other names." >&2
	exit 2
}

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
cp -R "$ld" "$out/apeld"
rm -f "$out/apeld/bin/"*

if ! (cd "$out/apeld" && ./build.sh) >"$out/build.log" 2>&1; then
	echo "apeld-reproducible: the build failed" >&2
	cat "$out/build.log" >&2
	exit 1
fi

rc=0
for f in "$ld/bin/"*; do
	n=${f##*/}
	if [ ! -f "$out/apeld/bin/$n" ]; then
		echo "apeld-reproducible: the build produced no $n" >&2
		rc=1
	elif ! cmp -s "$f" "$out/apeld/bin/$n"; then
		echo "apeld-reproducible: $n differs from what its source builds" >&2
		echo "  committed: $(sha256sum <"$f" | cut -d' ' -f1)" >&2
		echo "  rebuilt:   $(sha256sum <"$out/apeld/bin/$n" | cut -d' ' -f1)" >&2
		rc=1
	fi
done
for f in "$out/apeld/bin/"*; do
	n=${f##*/}
	[ -f "$ld/bin/$n" ] || {
		echo "apeld-reproducible: the build produced $n, which is not committed" >&2
		rc=1
	}
done

[ "$rc" -eq 0 ] && echo "apeld-reproducible: every loader rebuilds byte-for-byte"
exit "$rc"
