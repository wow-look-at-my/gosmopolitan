# Enforces the no-wordspam rule on every push. The scanner measures: a
# markdown size budget, a paragraph word cap, a comment-run line cap, and
# changelog phrasing by name.
#
# Markdown is scanned by glob, so a new doc is covered the day it lands.
# Go sources are named: the fork's own files only. An upstream file the
# fork merely tagged carries upstream's prose, which is not ours to cut.
tests:
	- desc: no wordspam in the fork's own markdown
	  cmd: dats/no-wordspam.sh *.md docs/*.md dats/*.dats dats/*.sh
	  exit: 0

	- desc: no wordspam in the cosmo runtime
	  cmd: dats/no-wordspam.sh src/runtime/*cosmo*.go
	  exit: 0

	- desc: no wordspam in the cosmo syscall emulation
	  cmd: dats/no-wordspam.sh src/internal/runtime/syscall/cosmo/*.go src/syscall/*cosmo*.go src/internal/poll/sendfile_shape*.go
	  exit: 0

	- desc: no wordspam in the fork's own std packages
	  cmd: dats/no-wordspam.sh src/internal/ape/*.go src/cmd/internal/cosmoape/*.go src/cmd/internal/objfile/ape.go src/cmd/link/internal/ld/ape*.go
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
