#!/usr/bin/env bash
# Copyright The Go Authors. All rights reserved. Use of this source code is
# governed by a BSD-style license that can be found in the LICENSE file.

# Move every org submodule onto the head of the branch it follows.

set -euo pipefail

cd "$(dirname "$0")/.."
[[ -f .gitmodules ]] || exit 0

here=${1:-}
if [[ -z "$here" ]]; then
	here=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo HEAD)
	[[ "$here" == HEAD ]] && here=""
fi

names=()
paths=()
urls=()
missing=()
while read -r key _; do
	name=${key#submodule.}
	name=${name%.path}

	path=$(git config -f .gitmodules --get "submodule.$name.path")
	url=$(git config -f .gitmodules --get "submodule.$name.url")
	case "$url" in
	*/github.com/wow-look-at-my/*) ;;
	*) continue ;;
	esac
	names+=("$name")
	paths+=("$path")
	urls+=("$url")
	[[ -e "$path/.git" ]] || missing+=("$path")
done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' || true)

# This call writes .git/config, so it runs before the jobs below start.
if ((${#missing[@]})); then
	git submodule update --init --jobs 16 -- "${missing[@]}" >/dev/null 2>&1 || true
fi

picked=$(mktemp -d)
trap 'rm -rf "$picked"' EXIT

# Each job touches only its own submodule's repository, so the jobs run at once.
track() {
	local idx=$1 name=${names[$1]} path=${paths[$1]} url=${urls[$1]} branch said
	branch=$(git config -f .gitmodules --get "submodule.$name.branch" || true)
	if [[ -n "$here" ]]; then
		# A probe that cannot reach the remote answers the same as one that
		# reached it and found no such branch, so it says which happened.
		if said=$(git ls-remote --exit-code --heads "$url" "refs/heads/$here" 2>&1); then
			branch=$here
		elif [[ -n "$said" ]]; then
			echo "submodulebranch: $path cannot ask $url for $here: $said" >&2
		fi
	fi
	[[ -n "$branch" ]] || return 0
	echo "$branch" >"$picked/$idx"

	# git says why it could not update. A build that keeps the gitlink
	# instead of the branch head is a build compiling a version nobody chose.
	if said=$(git -C "$path" fetch --depth 1 origin "+refs/heads/$branch:refs/remotes/origin/$branch" 2>&1) &&
		said=$(git -C "$path" checkout -q --detach "refs/remotes/origin/$branch" 2>&1); then
		echo "submodulebranch: $path at $branch $(git -C "$path" rev-parse --short=12 HEAD)" >&2
	else
		echo "submodulebranch: $path stays where it is: $url answered:" >&2
		echo "$said" >&2
	fi
	# The dependency's own submodules hold files its tests read, at the commits it names.
	if ! said=$(git -C "$path" submodule update --init --recursive 2>&1); then
		echo "submodulebranch: $path cannot check out its own submodules:" >&2
		echo "$said" >&2
	fi
}

for idx in "${!names[@]}"; do
	track "$idx" &
done
wait

for idx in "${!names[@]}"; do
	[[ -f "$picked/$idx" ]] || continue
	git config "submodule.${names[$idx]}.branch" "$(cat "$picked/$idx")"
done
