#!/usr/bin/env bash
# Copyright The Go Authors. All rights reserved. Use of this source code is
# governed by a BSD-style license that can be found in the LICENSE file.

# Check out every submodule at the commit its gitlink names, all at once.
# git submodule update fetches a commit that is not a branch tip one
# submodule at a time.

set -euo pipefail

cd "$(dirname "$0")/.."
[[ -f .gitmodules ]] || exit 0

git submodule --quiet init

failed=$(mktemp -d)
trap 'rm -rf "$failed"' EXIT

# Each job touches only its own submodule's repository, so the jobs run at once.
fetch() {
	local name=$1 path=$2 url commit said
	url=$(git config -f .gitmodules --get "submodule.$name.url")
	commit=$(git rev-parse "HEAD:$path")
	if [[ -e "$path/.git" ]] && [[ "$(git -C "$path" rev-parse HEAD 2>/dev/null)" == "$commit" ]]; then
		return 0
	fi
	if ! said=$({
		git init -q "$path" &&
			{ git -C "$path" remote get-url origin >/dev/null 2>&1 || git -C "$path" remote add origin "$url"; } &&
			git -C "$path" fetch -q --depth 1 --no-tags origin "$commit" &&
			git -C "$path" checkout -q --detach FETCH_HEAD
	} 2>&1); then
		echo "submodulefetch: $path cannot check out $commit from $url:" >&2
		echo "$said" >&2
		: >"$failed/$name"
	fi
}

while read -r key path; do
	name=${key#submodule.}
	name=${name%.path}
	fetch "$name" "$path" &
done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$')
wait

if [[ -n "$(ls -A "$failed")" ]]; then
	exit 1
fi
# The layout git submodule update leaves: each repository under .git/modules.
# absorbgitdirs names each move it makes even with --quiet, so only a failure prints.
if ! said=$(git submodule --quiet absorbgitdirs 2>&1); then
	echo "$said" >&2
	exit 1
fi
