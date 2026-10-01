#!/bin/sh
# Move every org submodule onto the head of its tracking branch, so a build
# uses current code rather than whatever commit the gitlink froze.
#
# The branch is this repository's own branch name when the submodule has one,
# else that submodule's default branch. `branch = .` in .gitmodules declares
# the first half; git resolves it only under `update --remote`, and an
# actions/checkout never passes that.
#
# A stale gitlink is not a version. It is a fix that never shipped: the
# cacheclient index lifetime sat unreached behind one for a whole day.
set -eu

branch=${1:-}
if [ -z "$branch" ]; then
	branch=$(git rev-parse --abbrev-ref HEAD)
fi

# retag rewrites one module's pseudo-version wherever vendor mode records it.
# The module path may carry a package suffix, so the version token is what
# gets located rather than the path. The shell puts every other part of the
# line back untouched, where awk would rebuild it and eat the leading tab.
retag() {
	want=$1
	tag=$2
	for file in src/cmd/go.mod src/cmd/vendor/modules.txt; do
		out=${file}.track
		: >"$out"
		while IFS= read -r line || [ -n "$line" ]; do
			case $line in
			*"${want}"*" v0.0.0-"*)
				pre=${line%%" v0.0.0-"*}
				post=${line#*" v0.0.0-"}
				extra=""
				case $post in
				*" "*) extra=" ${post#* }" ;;
				esac
				printf '%s %s%s\n' "$pre" "$tag" "$extra" >>"$out"
				;;
			*)
				printf '%s\n' "$line" >>"$out"
				;;
			esac
		done <"$file"
		mv "$out" "$file"
	done
}

git config --file .gitmodules --get-regexp '^submodule\..*\.path$' | while read -r key path; do
	name=${key#submodule.}
	name=${name%.path}
	track=$(git config --file .gitmodules --get "submodule.${name}.branch" || echo "")
	if [ "$track" != "." ]; then
		continue
	fi
	git -C "$path" fetch --quiet origin
	if git -C "$path" rev-parse --verify --quiet "origin/${branch}" >/dev/null; then
		want=origin/${branch}
	else
		head=$(git -C "$path" symbolic-ref --quiet --short refs/remotes/origin/HEAD || echo origin/master)
		want=$head
	fi
	was=$(git -C "$path" rev-parse HEAD)
	git -C "$path" checkout --quiet --detach "$want"
	now=$(git -C "$path" rev-parse HEAD)
	if [ "$was" = "$now" ]; then
		echo "submodule ${path} already at ${want} ${now}"
	else
		echo "submodule ${path} ${was} -> ${want} ${now}"
	fi
	# Vendor mode refuses to build when go.mod, modules.txt and the tree
	# disagree, so the pseudo-version follows the commit rather than
	# waiting for somebody to retype it.
	stamp=$(git -C "$path" show -s --format=%cd --date=format:'%Y%m%d%H%M%S' HEAD)
	short=$(echo "$now" | cut -c1-12)
	retag "github.com/wow-look-at-my/$(basename "$path")" "v0.0.0-${stamp}-${short}"
done
