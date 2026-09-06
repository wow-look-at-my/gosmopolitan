# Enforces the no-wordspam rule on every push. The scanner measures: a
# markdown size budget, a paragraph word cap, a comment-run line cap, and
# changelog phrasing by name.
#
# The paths listed are the ones the rule has been brought up to. Adding a
# path here is how the rule spreads; removing one is not.
tests:
	- desc: no wordspam in the hook tree
	  cmd: .claude/hooks/no-wordspam.sh .claude/hooks/no-wordspam.sh dats/no-wordspam.dats
	  exit: 0

	- desc: no wordspam in the cosmo syscall emulation
	  cmd: .claude/hooks/no-wordspam.sh src/runtime/os_cosmo_nt_statfs.go src/internal/runtime/syscall/cosmo/termios_cosmo.go src/internal/runtime/syscall/cosmo/darwinabi_cosmo.go src/syscall/bigbuf_cosmo.go
	  exit: 0

	- desc: the scanner refuses a paragraph over the cap
	  cmd: printf 'x %.0s' $(seq 1 200) > "$TMPDIR/spam.md"; .claude/hooks/no-wordspam.sh "$TMPDIR/spam.md"; test $? -eq 2
	  exit: 0
