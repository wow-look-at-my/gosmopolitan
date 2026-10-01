#!/usr/bin/env bash
# no-wordspam.sh FILE... -- refuse prose nobody asked for.
#
# It measures. It does not judge style:
#   1. A markdown file may not exceed MAX_MD_BYTES.
#   2. A markdown paragraph may not exceed MAX_PARA words.
#   3. A run of adjacent comment lines may not exceed MAX_COMMENT lines.
#   4. Changelog phrasing is refused by name.
#
# dats/source/no-wordspam.dats runs it over the tree, so CI enforces the rule.
# It lives here, in the repo, for exactly that reason: a check that runs
# a file the checkout does not contain enforces nothing.

set -uo pipefail

MAX_PARA=${WORDSPAM_MAX_PARA:-120}
MAX_COMMENT=${WORDSPAM_MAX_COMMENT:-12}
MAX_MD_BYTES=${WORDSPAM_MAX_MD_BYTES:-40000}

if [ "$#" -eq 0 ]; then
	echo "usage: ${0##*/} FILE..." >&2
	exit 2
fi
if [ "$#" -gt 1 ]; then
	rc=0
	for f in "$@"; do
		"$0" "$f" || rc=1
	done
	exit "$rc"
fi

path=$1
[ -f "$path" ] || exit 0
new=$(cat "$path")
tab=$(printf '\t')

# skipBlanks strips leading spaces and tabs, leaving the rest in `trimmed`.
skipBlanks() {
	trimmed=$1
	while :; do
		case $trimmed in
		" "*) trimmed=${trimmed# } ;;
		"$tab"*) trimmed=${trimmed#"$tab"} ;;
		*) return 0 ;;
		esac
	done
}

# structural reports whether a trimmed line opens a list, heading, table,
# quote or fence rather than prose.
structural() {
	case $1 in
	'|'* | '>'* | '#'* | '*'* | '+'* | '-'* | '```'*) return 0 ;;
	esac
	digits=${1%%[!0-9]*}
	[ -n "$digits" ] || return 1
	case ${1#"$digits"} in
	.*) return 0 ;;
	esac
	return 1
}

# paraFlush ends a paragraph and keeps its word count when it was prose.
paraFlush() {
	if [ "$skip" -eq 0 ] && [ "$words" -gt "$worst" ]; then
		worst=$words
	fi
	words=0
	skip=0
}

# worstParagraph prints the longest prose paragraph of a file, in words.
# A blank line ends a paragraph, and so does any structural line: a list
# item, a heading, a table row, a quote or a fence. A tight list is a
# list, not one long paragraph.
worstParagraph() {
	src=$1
	worst=0
	words=0
	skip=0
	while IFS= read -r text || [ -n "$text" ]; do
		skipBlanks "$text"
		if [ -z "$trimmed" ]; then
			paraFlush
			continue
		fi
		if structural "$trimmed"; then
			paraFlush
			skip=1
		fi
		# Word splitting counts the fields, so globbing stays off here.
		set -f
		set -- $text
		words=$((words + $#))
		set +f
	done <"$src"
	paraFlush
	printf '%d\n' "$worst"
}

fail() {
	printf 'BLOCKED (wordspam): %s\n\n' "$1" >&2
	printf 'Cut it, do not relocate it. Prose about what changed and why belongs\n' >&2
	printf 'in the commit message. A file carries current truth only.\n' >&2
	exit 2
}

# Phrases that only ever introduce a changelog. Ordinary English stays out
# of this list: a guard that cries wolf gets skimmed, and then it is worth
# nothing on the run that matters.
banned='was: |(it|this|that|which|there|one|they) used to |used to be |previously,|in this wave|the wave added|this text states|nothing tested it|is not a measurement|the old (stub|code|comment)|before (this|that) (branch|wave|change)'

case "$path" in
*.md)
	n=$(printf '%s' "$new" | wc -c)
	[ "$n" -le "$MAX_MD_BYTES" ] ||
		fail "$path would be $n bytes, over the $MAX_MD_BYTES budget. Extract or delete."

	worst=$(worstParagraph "$path")
	[ "$worst" -le "$MAX_PARA" ] ||
		fail "$path has a $worst-word paragraph, over the $MAX_PARA-word cap."
	;;
esac

# WORDSPAM-SELF: the line defining the pattern matches it, so it is skipped.
hit=$(printf '%s\n' "$new" | grep -v 'WORDSPAM-SELF' | grep -v '^banned=' |
	grep -inE "^[[:space:]]*(//|#|::|\*|--)?[[:space:]]*.*($banned)" | head -3 || true)
[ -z "$hit" ] || fail "changelog phrasing in $path:"$'\n'"$hit"

# commentRun prints the longest run of adjacent comment lines. A compiler
# or generator directive is not prose and does not count: a //sys block is
# the declaration of a syscall, and //go:nosplit is a property of the
# function under it. Neither breaks the run either, so prose cannot hide
# behind one.
commentRun() {
	src=$1
	worst=0
	run=0
	while IFS= read -r text || [ -n "$text" ]; do
		skipBlanks "$text"
		case $trimmed in
		"//go:"* | "//nolint"* | "//cgo_"* | \
			"//sys "* | "//sys$tab"* | "//sysnb "* | "//sysnb$tab"* | \
			"//export "* | "//export$tab"* | "//line "* | "//line$tab"* | \
			"//extern "* | "//extern$tab"*)
			continue
			;;
		esac
		case $trimmed in
		"//"* | "#"* | "::"*)
			run=$((run + 1))
			[ "$run" -le "$worst" ] || worst=$run
			continue
			;;
		esac
		run=0
	done <"$src"
	printf '%d\n' "$worst"
}

runlen=$(commentRun "$path")
case "$path" in
*.md | *.txt) ;;
*)
	[ "$runlen" -le "$MAX_COMMENT" ] ||
		fail "$path has a $runlen-line comment block, over the $MAX_COMMENT-line cap."
	;;
esac

exit 0
