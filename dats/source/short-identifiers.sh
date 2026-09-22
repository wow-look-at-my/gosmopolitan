#!/usr/bin/env bash
# short-identifiers.sh FILE... -- refuse a name shorter than three characters.
#
# Reads four declaration forms: a short variable declaration, a loop or range
# variable, a var declaration, and a method receiver. A parameter list needs a
# parser, so it is NOT read: a wrong answer there teaches a reader to skip the
# whole check. `_` is not a name. The .dats names the fork's own files only.

set -uo pipefail

MIN_LEN=${SHORT_IDENT_MIN_LEN:-3}

if [ "$#" -eq 0 ]; then
	echo "usage: ${0##*/} FILE..." >&2
	exit 2
fi
if [ "$#" -gt 1 ]; then
	rc=0
	for each in "$@"; do
		"$0" "$each" || rc=1
	done
	exit "$rc"
fi

path=$1
[ -f "$path" ] || exit 0

found=0
tab=$(printf '\t')

# isName reports whether the word is a plain Go identifier.
isName() {
	case $1 in
	"" | _) return 1 ;;
	*[!A-Za-z0-9_]*) return 1 ;;
	[0-9]*) return 1 ;;
	*) return 0 ;;
	esac
}

# check reports the word when it is a name under the minimum.
check() {
	isName "$1" || return 0
	[ "${#1}" -lt "$MIN_LEN" ] || return 0
	printf '%s:%d: %s is %d characters, and a name takes at least %d\n' \
		"$path" "$2" "$1" "${#1}" "$MIN_LEN"
	found=1
}

# names splits a comma-separated declaration list and checks each entry.
names() {
	rest=$1
	at=$2
	while [ -n "$rest" ]; do
		one=${rest%%,*}
		if [ "$one" = "$rest" ]; then
			rest=
		else
			rest=${rest#*,}
		fi
		one=${one## }
		one=${one%% }
		one=${one#"$tab"}
		one=${one%"$tab"}
		check "$one" "$at"
	done
}

num=0
while IFS= read -r raw || [ -n "$raw" ]; do
	num=$((num + 1))

	text=$raw
	while :; do
		case $text in
		" "*) text=${text# } ;;
		"$tab"*) text=${text#"$tab"} ;;
		*) break ;;
		esac
	done

	# A comment declares nothing.
	case $text in
	"//"* | "/*"* | "*"*) continue ;;
	esac

	# func (ptr *T) Name()
	case $text in
	"func ("*)
		recv=${text#func (}
		recv=${recv%%)*}
		check "${recv%% *}" "$num"
		continue
		;;
	esac

	# A grouped `var (` block declares nothing on its own line.
	case $text in
	"var ("*) : ;;
	"var "*)
		decl=${text#var }
		names "${decl%%[!A-Za-z0-9_,$tab ]*}" "$num"
		continue
		;;
	esac

	# A loop variable reads like a short declaration once `for` goes.
	case $text in
	"for "*) text=${text#for } ;;
	esac

	# Everything left of := is a plain name list.
	case $text in
	*":="*)
		lhs=${text%%:=*}
		names "$lhs" "$num"
		;;
	esac
done <"$path"

[ "$found" -eq 0 ] || exit 2
exit 0
