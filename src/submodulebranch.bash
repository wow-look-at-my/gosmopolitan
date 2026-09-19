#!/usr/bin/env bash
# Copyright 2026 The Go Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Move every org submodule onto the head of the branch it follows.
#
# A pair of repositories developed in tandem carry the same branch name, so an
# org submodule follows this repository's branch when it has one, and the branch
# named for it in .gitmodules otherwise. Both halves are a question git already
# knows how to ask: `git submodule update --init --remote` reads the branch to
# follow from submodule.<name>.branch. So the only thing done here is to answer
# the first half for git, by naming this repository's branch as the submodule's
# branch before the update runs. A detached HEAD names no branch, and the
# submodule keeps the one .gitmodules gives it.
#
# Nothing writes a version down. The go.mod file and vendor/modules.txt record a
# placeholder, and cmd/go reads the branch head of an org module by itself, so
# neither file is rewritten here and neither moves when a dependency's branch
# does.
#
# A remote this cannot reach leaves the checkout alone, so a build with no
# network reads what it already has.
#
# The branch may be given as the first argument, for a caller whose checkout is
# detached: a CI run knows the ref it is on, and git there does not.

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
	# git says why it could not update, and a build that keeps the gitlink
	# instead of the branch head is a build compiling a version nobody chose.
	if said=$(git submodule update --init --remote -- "$path" 2>&1); then
		echo "submodulebranch: $path at $branch $(git -C "$path" rev-parse --short=12 HEAD)" >&2
	else
		echo "submodulebranch: $path stays where it is: $url answered:" >&2
		echo "$said" >&2
	fi
done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' || true)
