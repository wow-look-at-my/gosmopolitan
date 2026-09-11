#!/usr/bin/env bash
# asm-tail-jmp-noframe.sh -- refuse a cosmo asm TEXT that tail-jumps to another
# symbol while the assembler gives it a frame.
#
# obj6 and obj7 give a frame to any TEXT that is not NOFRAME and makes a call:
# a PUSHQ BP on amd64, an LR save on arm64. The epilogue at each RET pops it.
# A tail JMP has no epilogue. The callee then reads its arguments one slot low,
# and its own RET pops the saved register, which holds a STACK address.
#
# runtime.write1 shipped that shape. Its darwin branch calls
# cosmo_xlat_errno_ax, so the whole function got a PUSHQ BP, and its NT branch
# tail-jumped to ntwrite1tramp. Every runtime print on an NT host then jumped
# into its own stack and died of a DEP access violation, which cost the panic
# report of every fatal error on that host.

set -uo pipefail

root=${1:-.}
fail=0

# One awk pass per file. A TEXT block ends at the next TEXT or at end of file.
# The hazard needs all three: a tail JMP to a symbol, a call in the body, and
# no NOFRAME. A call-free TEXT is a leaf, which both assemblers leave frameless.
scan() {
	awk -v file="$1" '
	function report() {
		if (name != "" && jmp != "" && hascall && !noframe) {
			printf "BLOCKED: framed TEXT tail-jumps to %s\n  %s:%d: %s\n", jmp, file, line, name > "/dev/stderr"
			bad++
		}
	}
	/^TEXT[ \t]/ {
		report()
		name = $0
		line = FNR
		noframe = (name ~ /NOFRAME/)
		hascall = 0
		jmp = ""
		next
	}
	name == "" { next }
	/^[ \t]*(CALL|BL|DUFFCOPY|DUFFZERO)[ \t]/ { hascall = 1 }
	/^[ \t]*(JMP|B)[ \t]+[^ \t]+\(SB\)[ \t]*$/ { jmp = $2 }
	END { report(); exit(bad ? 2 : 0) }
	' "$1"
}

while IFS= read -r f; do
	scan "$f" || fail=1
done < <(find "$root/src" -name '*cosmo*.s' 2>/dev/null | sort)

if [ "$fail" -ne 0 ]; then
	printf '\nA TEXT that tail-jumps must carry NOFRAME. The assembler adds a\n' >&2
	printf 'frame to any calling TEXT without it, and a JMP never pops one.\n' >&2
	exit 2
fi
