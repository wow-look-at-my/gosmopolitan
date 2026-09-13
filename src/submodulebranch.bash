#!/usr/bin/env bash
# Copyright 2026 The Go Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Move each submodule that follows this repository's branch onto that branch.
#
# A pair of repositories developed in tandem carry the same branch name. So a
# submodule follows the branch of this repository's name when it has one, and
# its default branch otherwise. The merge deletes the branch and both sides read
# the default branch again, so nothing has to be repointed.
#
# `branch = .` in .gitmodules says the first half to git. It has no second half:
# a submodule with no branch of that name makes `git submodule update --remote`
# fail rather than fall back.
#
# A remote this cannot reach leaves the checkout alone, so a build with no
# network reads what it already has.

set -euo pipefail

cd "$(dirname "$0")/.."
[[ -f .gitmodules ]] || exit 0

here=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo HEAD)
[[ "$here" == HEAD ]] && here=""

# git parses its own config, so nothing here reads .gitmodules by hand.
follows=$(git config -f .gitmodules --get-regexp '^submodule\..*\.branch$' 2>/dev/null || true)

while read -r key value; do
	[[ "$value" == "." ]] || continue
	name=${key#submodule.}
	name=${name%.branch}

	path=$(git config -f .gitmodules --get "submodule.$name.path")
	url=$(git config -f .gitmodules --get "submodule.$name.url")
	[[ -d "$path" ]] || continue

	# One ls-remote asks for the default branch and the matching one. A
	# submodule without the branch, and one whose default branch IS that
	# branch, both take the default.
	ref=HEAD
	if [[ -n "$here" ]]; then
		refs=$(git ls-remote --symref "$url" HEAD "refs/heads/$here" 2>/dev/null || true)
		default=$(awk '$1 == "ref:" && $3 == "HEAD" { sub("refs/heads/", "", $2); print $2 }' <<<"$refs")
		if grep -q "refs/heads/$here\$" <<<"$refs" && [[ "$default" != "$here" ]]; then
			ref="refs/heads/$here"
		fi
	fi

	if ! git -C "$path" fetch --quiet --depth 1 "$url" "$ref" 2>/dev/null; then
		echo "submodulebranch: $path stays where it is: cannot reach $url" >&2
		continue
	fi
	git -C "$path" checkout --quiet --detach FETCH_HEAD

	# go.mod and vendor/modules.txt both record the version, and the go
	# command refuses to build in vendor mode when the two disagree.
	module=$(awk '$1 == "module" { print $2; exit }' "$path/go.mod")
	stamp=$(git -C "$path" show -s --format=%cd --date=format-local:%Y%m%d%H%M%S HEAD)
	short=$(git -C "$path" rev-parse --short=12 HEAD)
	version="v0.0.0-$stamp-$short"

	# A repository can publish several modules, and a requirement names the
	# nested one. So the module this repository declares is a prefix of the
	# path the version files carry, not always the whole of it.
	for f in src/cmd/go.mod src/cmd/vendor/modules.txt; do
		[[ -f "$f" ]] || continue
		sed -i -E "s|(${module}(/[^[:space:]]+)?) v[0-9][^[:space:]]*|\1 $version|g" "$f"
	done
	echo "submodulebranch: $path at $version" >&2
done <<<"$follows"
