#!/bin/sh
# org-unpinned.sh FILE... -- refuse a frozen version of an org dependency.
#
# 1. A submodule under github.com/wow-look-at-my names a branch to follow,
#    which src/submodulebranch.bash hands to `git submodule update --remote`.
# 2. A line naming an org module carries no dated version: go.mod,
#    vendor/modules.txt and go.sum hold the placeholder `vN.0.0` for the
#    path's major, and cmd/go resolves it to the branch head in memory.
# 3. A step that `uses:` an org action takes a branch, never a tag or a sha.
#
# Exit: 0 nothing is pinned, 2 something is.
set -eu

tab=$(printf '\t')
rc=0

# placeholder reports whether a version token is the placeholder a version file
# records for an org module: vN.0.0, for any decimal major, with no pre-release
# part and no pseudo-version date.
placeholder() {
	case $1 in
	v[0-9]*) ;;
	*) return 1 ;;
	esac
	major=${1#v}
	major=${major%%.0.0}
	case $major in
	"" | *[!0-9]*) return 1 ;;
	esac
	case $1 in
	"v$major.0.0" | "v$major.0.0/go.mod") return 0 ;;
	esac
	return 1
}

# orgSubmodules names every submodule section whose url is under the org.
orgSubmodules() {
	git config --file "$1" --get-regexp '^submodule\..*\.url$' | while read -r key url; do
		case $url in
		*github.com/wow-look-at-my/*) ;;
		*) continue ;;
		esac
		name=${key#submodule.}
		printf '%s\n' "${name%.url}"
	done
}

# pinnedVersion prints the first version token on a line that is not the
# placeholder. A token is a version when it opens with v and a digit.
pinnedVersion() {
	set -f
	# shellcheck disable=SC2086
	set -- $1
	set +f
	for word in "$@"; do
		case $word in
		v[0-9]*) ;;
		*) continue ;;
		esac
		placeholder "$word" && continue
		printf '%s' "$word"
		return 0
	done
	return 1
}

# pinnedRef prints the ref of a `uses:` line when that ref is a tag or a sha.
pinnedRef() {
	rest=${1#*wow-look-at-my/}
	case $rest in
	*@*) ;;
	*) return 1 ;;
	esac
	ref=${rest#*@}
	ref=${ref%%"$tab"*}
	ref=${ref%% *}
	case $ref in
	v[0-9]*)
		printf '%s' "$ref"
		return 0
		;;
	esac
	hex=${ref%%[!0-9a-f]*}
	if [ "${#ref}" -eq 40 ] && [ "${#hex}" -eq 40 ]; then
		printf '%s' "$ref"
		return 0
	fi
	return 1
}

# report prints one finding and remembers that the run has failed.
report() {
	printf '%s\n' "$1" >&2
	rc=2
}

for path in "$@"; do
	[ -f "$path" ] || continue
	if grep -q '^\[submodule ' "$path"; then
		for name in $(orgSubmodules "$path"); do
			track=$(git config --file "$path" --get "submodule.${name}.branch" || echo "")
			if [ -z "$track" ]; then
				report "$(printf '%s: submodule %s names no branch to follow' "$path" "$name")"
			fi
		done
	fi
	num=0
	# A redirected loop runs in this shell, so a finding inside it is kept.
	while IFS= read -r text || [ -n "$text" ]; do
		num=$((num + 1))
		if [ "${text#*wow-look-at-my/}" = "$text" ]; then
			continue
		fi
		if [ "${text#*uses:}" != "$text" ]; then
			if ref=$(pinnedRef "$text"); then
				report "$(printf '%s:%d: org action pinned to %s' "$path" "$num" "$ref")"
			fi
			continue
		fi
		if version=$(pinnedVersion "$text"); then
			report "$(printf '%s:%d: org module pinned to %s' "$path" "$num" "$version")"
		fi
	done <"$path"
done

if [ "$rc" -ne 0 ]; then
	cat >&2 <<'EOF'

A pin freezes this repository against one commit of another and calls the
result reproducible. Let the submodule follow a branch instead, and leave the
placeholder to the version files: cmd/go resolves it to that branch's head.
EOF
fi
exit "$rc"
