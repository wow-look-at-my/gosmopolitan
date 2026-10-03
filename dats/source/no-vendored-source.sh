#!/usr/bin/env bash
# no-vendored-source.sh [REPO] -- refuse copied dependency source.
#
# A vendor path holds a git submodule, never files this repo tracks. A
# submodule records one commit of an upstream repository, so a bump is a
# pointer move and a fix goes back where the code lives. Copied source
# writes another repository down a second time, under a path this one then
# owns, and every later merge pays for it.
#
# Two things are checked:
#   1. This repo tracks no source file under a vendor tree.
#   2. Every module named in a modules.txt sits under a gitlink.

set -uo pipefail

repo=${1:-.}
cd "$repo" || exit 2

fail() {
	printf 'BLOCKED (vendored source): %s\n\n' "$1" >&2
	printf 'A vendor path holds a submodule. Run git submodule add for the\n' >&2
	printf 'upstream repository, then write the require and the modules.txt\n' >&2
	printf 'entry. Never run go mod vendor here.\n' >&2
	exit 2
}

gitlinks=$(git ls-files -s -- 'src/vendor' 'src/cmd/vendor' 2>/dev/null | grep '^160000 ' | cut -f2)

# A tracked source file under a vendor tree is copied source by definition:
# a submodule's own files belong to that submodule, not to this repo.
copied=$(git ls-files -- 'src/vendor/*.go' 'src/cmd/vendor/*.go' 2>/dev/null | head -5)
if [ -n "$copied" ]; then
	fail "these source files are tracked under a vendor tree:"$'\n'"$copied"
fi

missing=
for tree in src/vendor src/cmd/vendor; do
	[ -f "$tree/modules.txt" ] || continue
	while IFS= read -r line; do
		case $line in
		'# '*) ;;
		*) continue ;;
		esac
		rest=${line#\# }
		modpath=${rest%% *}
		found=
		while IFS= read -r link; do
			[ -n "$link" ] || continue
			# The gitlink is the repository. A module can be rooted in a
			# subdirectory of it, so the gitlink is a prefix of the path.
			case "$tree/$modpath" in
			"$link" | "$link"/*) found=yes ;;
			esac
			# A /vN module path can also sit under the gitlink's parent.
			case "$link" in
			"$tree/$modpath"/*) found=yes ;;
			esac
		done <<<"$gitlinks"
		[ -n "$found" ] || missing="$missing$tree/$modpath"$'\n'
	done <"$tree/modules.txt"
done

if [ -n "$missing" ]; then
	fail "these modules have no submodule:"$'\n'"$missing"
fi

exit 0
