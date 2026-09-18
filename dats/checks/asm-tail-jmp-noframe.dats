# The assembler frames any calling TEXT that is not NOFRAME, and a tail JMP
# never pops that frame. runtime.write1 shipped the shape: its NT branch
# jumped into ntwrite1tramp over a stray PUSHQ BP, so the trampoline returned
# through the caller's frame pointer and every runtime print on NT died.
tests:
	- desc: no framed cosmo TEXT tail-jumps to another symbol
	  cmd: dats/checks/asm-tail-jmp-noframe.sh .
	  exit: 0

	- desc: the guard refuses the shape that shipped
	  cmd: mkdir -p "$TMPDIR/a/src/runtime" && printf 'TEXT runtime·w(SB),NOSPLIT,$0-28\n\tJMP\truntime·t(SB)\nlate:\n\tCALL\truntime·x(SB)\n\tRET\n' > "$TMPDIR/a/src/runtime/sys_cosmo_amd64.s"; dats/checks/asm-tail-jmp-noframe.sh "$TMPDIR/a"; test $? -eq 2
	  exit: 0

	- desc: NOFRAME is the fix, so the guard accepts it
	  cmd: mkdir -p "$TMPDIR/b/src/runtime" && printf 'TEXT runtime·w(SB),NOSPLIT|NOFRAME,$0-28\n\tJMP\truntime·t(SB)\nlate:\n\tCALL\truntime·x(SB)\n\tRET\n' > "$TMPDIR/b/src/runtime/sys_cosmo_amd64.s"; dats/checks/asm-tail-jmp-noframe.sh "$TMPDIR/b"
	  exit: 0

	- desc: a call-free TEXT is a leaf, which the assembler leaves frameless
	  cmd: mkdir -p "$TMPDIR/c/src/runtime" && printf 'TEXT runtime·w(SB),NOSPLIT,$0-4\n\tJMP\truntime·t(SB)\n' > "$TMPDIR/c/src/runtime/sys_cosmo_amd64.s"; dats/checks/asm-tail-jmp-noframe.sh "$TMPDIR/c"
	  exit: 0
