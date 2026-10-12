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

while read -r key _; do
	name=${key#submodule.}
	name=${name%.path}

	path=$(git config -f .gitmodules --get "submodule.$name.path")
	url=$(git config -f .gitmodules --get "submodule.$name.url")
	case "$url" in
	*/github.com/wow-look-at-my/*) ;;
	*) continue ;;
	esac

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
	[[ -n "$branch" ]] || continue

	git config "submodule.$name.branch" "$branch"
	# A checkout clones a submodule shallow, against a refspec holding the commit the gitlink names.
	git submodule update --init -- "$path" >/dev/null 2>&1 || true
	if [[ -d "$path/.git" || -f "$path/.git" ]]; then
		git -C "$path" fetch --depth 1 origin \
			"+refs/heads/$branch:refs/remotes/origin/$branch" >/dev/null 2>&1 || true
	fi
	# git says why it could not update. A build that keeps the gitlink
	# instead of the branch head is a build compiling a version nobody chose.
	if said=$(git submodule update --init --remote -- "$path" 2>&1); then
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
done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' || true)
