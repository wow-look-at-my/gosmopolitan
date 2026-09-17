#!/bin/sh
# ape-identical.sh -- compare the APE binaries each build leg made, origin
# against origin. An APE is host-independent by construction, so the Linux,
# macOS and Windows legs must emit the same bytes. A difference means a build
# input differed between the hosts, not that the format varies.
#
# An argument names another directory of ape-binary-<origin> trees, which is
# how the negative cases get a fabricated tree. The default is the binaries
# directory the test job downloads into, so run this from the repository root.
# A binary missing from any origin fails: comparing the origins that happen to
# be present proves nothing.
set -u

base=${1:-binaries}
origins='Linux macOS Windows'
files='fizzbuzz.com runtimeprobe.com fizzbuzz-tri.com runtimeprobe-tri.com fizzbuzz-amd.com runtimeprobe-amd.com'

# macOS carries shasum instead of sha256sum.
sum() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum <"$1" | cut -d' ' -f1
	else
		shasum -a 256 <"$1" | cut -d' ' -f1
	fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

rc=0
for f in $files; do
	missing=
	for o in $origins; do
		[ -f "$base/ape-binary-$o/$f" ] || missing="$missing $o"
	done
	if [ -n "$missing" ]; then
		echo "ape-identical: $f is missing from these origins:$missing" >&2
		echo "  looked under $base/ape-binary-<origin>/$f" >&2
		rc=1
		continue
	fi
	for pair in Linux:macOS Linux:Windows macOS:Windows; do
		a=${pair%%:*}
		b=${pair##*:}
		pa="$base/ape-binary-$a/$f"
		pb="$base/ape-binary-$b/$f"
		cmp -s "$pa" "$pb" && continue
		echo "ape-identical: $f differs between the $a and $b origins" >&2
		cmp "$pa" "$pb" 2>&1 | sed 's/^/  /' >&2
		diffs=$(cmp -l "$pa" "$pb" 2>/dev/null)
		n=$(printf '%s' "$diffs" | grep -c .)
		echo "  $n differing offsets in the common prefix, first 8:$(printf '%s\n' "$diffs" | awk 'NR<=8{printf " %s", $1}')" >&2
		echo "  sizes: $a $(wc -c <"$pa" | tr -d ' ') bytes, $b $(wc -c <"$pb" | tr -d ' ') bytes" >&2
		echo "  $a: $(sum "$pa")" >&2
		echo "  $b: $(sum "$pb")" >&2
		# A string one binary holds and the other does not names the cause. A
		# host's own checkout path is the usual one, and -trimpath is its fix.
		echo "  strings in one and not the other, first 20 (< $a, > $b):" >&2
		diff "$(strings -n 8 "$pa" | sort -u > "$tmp/l"; echo "$tmp/l")" \
			"$(strings -n 8 "$pb" | sort -u > "$tmp/r"; echo "$tmp/r")" |
			grep '^[<>]' | head -20 | sed 's/^/    /' >&2
		rc=1
	done
done

[ "$rc" -eq 0 ] && echo "ape-identical: every APE is identical across the Linux, macOS and Windows origins"
exit "$rc"
