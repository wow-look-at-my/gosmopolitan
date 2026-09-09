#!/usr/bin/env bash
# no-wordspam refuses prose that has outgrown the thing it describes.
#
# Usage: no-wordspam.sh FILE...
# Exit 0 when every file is clean. Exit 2 when a file breaks a rule.
# Exit 1 on a usage error or an unreadable file.
set -uo pipefail

MD_BYTES=${NO_WORDSPAM_MD_BYTES:-40000}
PARA_WORDS=${NO_WORDSPAM_PARA_WORDS:-150}
COMMENT_RUN=${NO_WORDSPAM_COMMENT_RUN:-20}

# A changelog sentence names a state the tree does not hold. Git history is
# where that belongs, so the scanner names each phrase and refuses it.
BANNED='used to|no longer|previously|was never requested|formerly|going forward'

if [ $# -eq 0 ]; then
	echo "usage: no-wordspam.sh FILE..." >&2
	exit 1
fi

rc=0

report() {
	echo "$1" >&2
	rc=2
}

for f in "$@"; do
	if [ ! -f "$f" ]; then
		echo "no-wordspam: no such file: $f" >&2
		exit 1
	fi

	# A comment marker of "" selects the markdown rules. Every other file is
	# scanned on its comment lines alone, so the code itself is never prose.
	case "$f" in
	*.md | *.markdown) marker='' ;;
	*.go | *.ts | *.js | *.c | *.h) marker='//' ;;
	*) marker='#' ;;
	esac

	if [ -z "$marker" ]; then
		size=$(wc -c <"$f")
		if [ "$size" -gt "$MD_BYTES" ]; then
			report "$f: $size bytes over the $MD_BYTES budget. Extract a section rather than trim one."
		fi
		# A fenced block is data, not prose, so the word cap stops at the fence.
		paras=$(awk -v cap="$PARA_WORDS" '
			function flush() { if (n > cap) print start ":" n; n = 0 }
			/^```/ { fence = !fence; flush(); next }
			fence { next }
			/^[ \t]*$/ { flush(); next }
			{ if (n == 0) start = NR; n += NF }
			END { flush() }
		' "$f")
		if [ -n "$paras" ]; then
			while IFS=: read -r line words; do
				echo "$f:$line: paragraph of $words words over the $PARA_WORDS cap" >&2
			done <<<"$paras"
			rc=2
		fi
		if grep -niE "$BANNED" "$f" >&2; then
			report "$f: changelog phrasing. State what the file is, not what it was."
		fi
		continue
	fi

	if [ "$marker" = '//' ]; then
		re='^[ \t]*//'
	else
		re='^[ \t]*#'
	fi

	runs=$(awk -v re="$re" -v cap="$COMMENT_RUN" '
		function flush() { if (run > cap) print start ":" run; run = 0 }
		$0 ~ re { if (run == 0) start = NR; run++; next }
		{ flush() }
		END { flush() }
	' "$f")
	if [ -n "$runs" ]; then
		while IFS=: read -r line len; do
			echo "$f:$line: $len consecutive comment lines over the $COMMENT_RUN cap" >&2
		done <<<"$runs"
		rc=2
	fi

	if grep -nE "$re" "$f" | grep -iE "$BANNED" >&2; then
		report "$f: changelog phrasing in a comment. State what the code is, not what it was."
	fi
done

exit $rc
