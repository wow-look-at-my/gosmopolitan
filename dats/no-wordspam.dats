# Enforces the no-wordspam rule on every push. The scanner measures: a
# markdown size budget, a paragraph word cap, a comment-run line cap, and
# changelog phrasing by name.
#
# The scan covers every markdown file this fork wrote, and the cosmo
# sources the rule has been brought up to. Adding a path is how the rule
# spreads; removing one is not.
#
# Two bodies are not in it yet, and docs/STUBS-INVENTORY.md tracks both:
# WASM_SHORTCOMINGS.md, over the byte budget by a changelog table, and
# about forty cosmo runtime and syscall files whose comment blocks run
# past the twelve-line cap.
tests:
	- desc: no wordspam in the fork's own markdown
	  cmd: .claude/hooks/no-wordspam.sh CLAUDE.md README.md docs/APE-BUILD.md docs/APE-STAGING.md docs/CI.md docs/INSTALL.md docs/LOOP-INLINING.md docs/OPTIONAL-PARAMS.md docs/PLATFORM-STATUS.md docs/STUBS-INVENTORY.md docs/TESTING-PARALLEL.md docs/UPREV-GO1.27.md docs/WASM.md dats/no-wordspam.dats .claude/hooks/no-wordspam.sh
	  exit: 0

	- desc: no wordspam in the cosmo syscall emulation
	  cmd: .claude/hooks/no-wordspam.sh src/runtime/os_cosmo_nt_statfs.go src/internal/runtime/syscall/cosmo/termios_cosmo.go src/internal/runtime/syscall/cosmo/darwinabi_cosmo.go src/syscall/bigbuf_cosmo.go src/internal/poll/sendfile_shape.go src/syscall/hostos_cosmo.go
	  exit: 0

	- desc: the scanner refuses a paragraph over the cap
	  cmd: printf 'x %.0s' $(seq 1 200) > "$TMPDIR/spam.md"; .claude/hooks/no-wordspam.sh "$TMPDIR/spam.md"; test $? -eq 2
	  exit: 0
