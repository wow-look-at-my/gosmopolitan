#!/bin/sh
# suite-shards.sh -- the suite legs split `go tool dist test` with
# -shard=K/N. This proves the split drops nothing.
#
#   lists N GOOS/GOARCH...  per target, parts 0/N..N-1/N of -list, put
#                           together, are the full -list, each test once.
#   matrix WORKFLOW...      each `shards: [N]` has `shard: [0, ..., N-1]`
#                           on the line above, so every part runs.
#
# SUITE_SHARDS_DIST replaces `go tool dist test`, for the guard's own tests.
# Exit: 0 the split is whole, 2 it is not.
set -u
export LC_ALL=C

tab=$(printf '\t')

# trim strips leading spaces and tabs, leaving the rest in `trimmed`.
trim() {
	trimmed=$1
	while :; do
		case $trimmed in
		" "*) trimmed=${trimmed# } ;;
		"$tab"*) trimmed=${trimmed#"$tab"} ;;
		*) return 0 ;;
		esac
	done
}

# partList prints "[0, 1, ..., N-1]".
partList() {
	text="[0"
	idx=1
	while [ "$idx" -lt "$1" ]; do
		text="$text, $idx"
		idx=$((idx + 1))
	done
	printf '%s]' "$text"
}

# listTests prints the -list of one target, sorted; extra arguments go to dist.
listTests() {
	target=$1
	shift
	GOOS=${target%/*} GOARCH=${target#*/} ${SUITE_SHARDS_DIST:-go tool dist test} -list "$@" | sort
}

checkLists() {
	count=$1
	shift
	case $count in
	"" | *[!0-9]* | 0)
		printf 'BLOCKED: want a part count of 1 or more, got %s\n' "$count" >&2
		return 2
		;;
	esac
	work=$(mktemp -d)
	rc=0
	while [ "$#" -gt 0 ]; do
		target=$1
		shift
		if ! listTests "$target" >"$work/full"; then
			printf 'BLOCKED: %s: dist test -list failed\n' "$target" >&2
			rc=2
			continue
		fi
		if [ ! -s "$work/full" ]; then
			printf 'BLOCKED: %s: dist test -list printed no tests\n' "$target" >&2
			rc=2
			continue
		fi
		: >"$work/parts"
		idx=0
		while [ "$idx" -lt "$count" ]; do
			if ! listTests "$target" "-shard=$idx/$count" >>"$work/parts"; then
				printf 'BLOCKED: %s: dist test -list -shard=%s/%s failed\n' "$target" "$idx" "$count" >&2
				rc=2
			fi
			idx=$((idx + 1))
		done
		sort "$work/parts" >"$work/joined"
		if cmp -s "$work/full" "$work/joined"; then
			printf '%s: %s tests, all in exactly one of %s parts\n' "$target" "$(wc -l <"$work/full" | tr -d ' ')" "$count"
			continue
		fi
		rc=2
		printf 'BLOCKED: %s: the %s parts are not the whole suite\n' "$target" "$count" >&2
		comm -23 "$work/full" "$work/joined" | while IFS= read -r name; do
			printf '  in no part:       %s\n' "$name" >&2
		done
		uniq -d "$work/joined" | while IFS= read -r name; do
			printf '  in several parts: %s\n' "$name" >&2
		done
		uniq "$work/joined" | comm -13 "$work/full" - | while IFS= read -r name; do
			printf '  not registered:   %s\n' "$name" >&2
		done
	done
	return "$rc"
}

checkMatrix() {
	rc=0
	pairs=0
	while [ "$#" -gt 0 ]; do
		file=$1
		shift
		num=0
		prev=""
		while IFS= read -r text || [ -n "$text" ]; do
			num=$((num + 1))
			trim "$text"
			case $trimmed in
			"shards: ["*"]")
				count=${trimmed#"shards: ["}
				count=${count%"]"}
				case $count in
				"" | *[!0-9]* | 0)
					printf '%s:%d: shards: wants one count of 1 or more\n' "$file" "$num" >&2
					rc=2
					;;
				*)
					pairs=$((pairs + 1))
					want="shard: $(partList "$count")"
					if [ "$prev" != "$want" ]; then
						printf '%s:%d: shards: [%s] needs "%s" on the line above, found "%s"\n' "$file" "$num" "$count" "$want" "$prev" >&2
						rc=2
					fi
					;;
				esac
				;;
			esac
			prev=$trimmed
		done <"$file"
	done
	if [ "$pairs" -eq 0 ]; then
		printf 'BLOCKED: no matrix sets shards: [N]; nothing says which parts run\n' >&2
		rc=2
	fi
	return "$rc"
}

mode=${1:-}
[ "$#" -eq 0 ] || shift
case $mode in
lists) checkLists "$@" ;;
matrix) checkMatrix "$@" ;;
*)
	printf 'usage: suite-shards.sh lists N GOOS/GOARCH... | matrix WORKFLOW...\n' >&2
	exit 2
	;;
esac
