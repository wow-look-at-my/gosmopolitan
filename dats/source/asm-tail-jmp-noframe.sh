#!/usr/bin/env bash
# asm-tail-jmp-noframe.sh -- refuse a cosmo asm TEXT that tail-jumps to another
# symbol while the assembler gives it a frame.
#
# obj6 and obj7 give a frame to any TEXT that is not NOFRAME and makes a call:
# a PUSHQ BP on amd64, an LR save on arm64. The epilogue at each RET pops it.
# A tail JMP has no epilogue. The callee then reads its arguments one slot low,
# and its own RET pops the saved register, which holds a STACK address.
#

set -uo pipefail

root=${1:-.}
fail=0
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

# report names the TEXT block the scan just finished, when it carries the
# hazard: a tail JMP to a symbol, a call in the body, and no NOFRAME.
report() {
	[ -n "$name" ] && [ -n "$jmp" ] && [ "$hascall" -eq 1 ] && [ "$noframe" -eq 0 ] || return 0
	printf 'BLOCKED: framed TEXT tail-jumps to %s\n  %s:%d: %s\n' "$jmp" "$file" "$line" "$name" >&2
	bad=$((bad + 1))
}

# One pass per file. A TEXT block ends at the next TEXT or at end of file.
# A call-free TEXT is a leaf, which both assemblers leave frameless.
scan() {
	file=$1
	name=""
	line=0
	num=0
	bad=0
	noframe=0
	hascall=0
	jmp=""
	while IFS= read -r text || [ -n "$text" ]; do
		num=$((num + 1))
		case $text in
		"TEXT "* | "TEXT$tab"*)
			report
			name=$text
			line=$num
			noframe=0
			case $name in
			*NOFRAME*) noframe=1 ;;
			esac
			hascall=0
			jmp=""
			continue
			;;
		esac
		[ -n "$name" ] || continue
		skipBlanks "$text"
		body=$trimmed
		head=${body%%" "*}
		head=${head%%"$tab"*}
		# A bare keyword with nothing after it is neither a call nor a jump.
		[ "$head" != "$body" ] || head=""
		case $head in
		CALL | BL | DUFFCOPY | DUFFZERO) hascall=1 ;;
		esac
		case $head in
		JMP | B)
			skipBlanks "${body#"$head"}"
			target=${trimmed%%" "*}
			target=${target%%"$tab"*}
			# Only blanks may follow the target, or this is not a tail jump.
			skipBlanks "${trimmed#"$target"}"
			case $target in
			?*"(SB)") [ -n "$trimmed" ] || jmp=$target ;;
			esac
			;;
		esac
	done <"$file"
	report
	[ "$bad" -eq 0 ] || return 2
	return 0
}

while IFS= read -r f; do
	scan "$f" || fail=1
done < <(find "$root/src" -name '*cosmo*.s' 2>/dev/null | sort)

if [ "$fail" -ne 0 ]; then
	printf '\nA TEXT that tail-jumps must carry NOFRAME. The assembler adds a\n' >&2
	printf 'frame to any calling TEXT without it, and a JMP never pops one.\n' >&2
	exit 2
fi
