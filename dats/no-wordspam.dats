# Enforces the no-wordspam rule on every push. The scanner measures: a
# markdown size budget, a paragraph word cap, a comment-run line cap, and
# changelog phrasing by name.
#
# The scan covers every markdown file this fork wrote and every cosmo
# source. Adding a path is how the rule spreads; removing one is not.
tests:
	- desc: no wordspam in the fork's own markdown
	  cmd: dats/no-wordspam.sh CLAUDE.md README.md docs/APE-BUILD.md docs/APE-STAGING.md docs/CI.md docs/INSTALL.md docs/LOOP-INLINING.md docs/OPTIONAL-PARAMS.md docs/PLATFORM-STATUS.md docs/STUBS-INVENTORY.md docs/TESTING-PARALLEL.md docs/UPREV-GO1.27.md docs/WASM.md WASM_SHORTCOMINGS.md dats/no-wordspam.dats dats/no-wordspam.sh
	  exit: 0

	- desc: no wordspam in the cosmo runtime
	  cmd: dats/no-wordspam.sh src/runtime/*cosmo*.go
	  exit: 0

	- desc: no wordspam in the cosmo syscall emulation
	  cmd: dats/no-wordspam.sh src/internal/runtime/syscall/cosmo/*.go src/syscall/*cosmo*.go src/internal/poll/sendfile_shape*.go
	  exit: 0

	- desc: the scanner refuses a paragraph over the cap
	  cmd: printf 'x %.0s' $(seq 1 200) > "$TMPDIR/spam.md"; dats/no-wordspam.sh "$TMPDIR/spam.md"; test $? -eq 2
	  exit: 0

	- desc: the scanner refuses a comment run over the cap
	  cmd: printf '// x\n%.0s' $(seq 1 20) > "$TMPDIR/spam.go"; dats/no-wordspam.sh "$TMPDIR/spam.go"; test $? -eq 2
	  exit: 0

	- desc: a //sys block is a declaration, not a comment run
	  cmd: printf '//sys\tF%s() (err error)\n' $(seq 1 30) > "$TMPDIR/sys.go"; dats/no-wordspam.sh "$TMPDIR/sys.go"
	  exit: 0
