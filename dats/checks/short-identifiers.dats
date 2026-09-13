# Enforces "a name is a word, not a letter" on every push. CLAUDE.md states
# the rule and nothing enforced it, which made it a convention rather than a
# requirement.
#
# Go sources are NAMED, never globbed: the fork's own files only. CLAUDE.md
# says not to rename upstream's receivers in a file this fork is not otherwise
# rewriting, so a glob would fail on code that is not ours to change.
tests:
	- desc: the fork's own cmd/go additions name their variables
	  cmd: dats/checks/short-identifiers.sh src/cmd/go/internal/load/testgroup.go src/cmd/go/internal/load/testgroupalone.go src/cmd/go/internal/load/generatedep.go src/cmd/go/internal/load/generatesandbox.go
	  exit: 0

	- desc: the scanner refuses a one-letter receiver
	  cmd: printf 'package p\n\nfunc (x *T) M() {}\n' > "$TMPDIR/recv.go"; dats/checks/short-identifiers.sh "$TMPDIR/recv.go"; test $? -eq 2
	  exit: 0

	- desc: the scanner refuses a one-letter range variable
	  cmd: printf 'package p\n\nfunc F() {\n\tfor i := range xs {\n\t\t_ = i\n\t}\n}\n' > "$TMPDIR/loop.go"; dats/checks/short-identifiers.sh "$TMPDIR/loop.go"; test $? -eq 2
	  exit: 0

	- desc: the scanner refuses a two-letter short declaration
	  cmd: printf 'package p\n\nfunc F() {\n\tok := g()\n\t_ = ok\n}\n' > "$TMPDIR/decl.go"; dats/checks/short-identifiers.sh "$TMPDIR/decl.go"; test $? -eq 2
	  exit: 0

	- desc: the scanner refuses a one-letter var declaration
	  cmd: printf 'package p\n\nvar b []byte\n' > "$TMPDIR/var.go"; dats/checks/short-identifiers.sh "$TMPDIR/var.go"; test $? -eq 2
	  exit: 0

	- desc: the blank identifier is not a name
	  cmd: printf 'package p\n\nfunc F() {\n\tfor _, item := range xs {\n\t\t_ = item\n\t}\n}\n' > "$TMPDIR/blank.go"; dats/checks/short-identifiers.sh "$TMPDIR/blank.go"
	  exit: 0

	- desc: a comment naming a short variable is not a declaration
	  cmd: printf 'package p\n\n// i is not declared here.\nfunc F() {}\n' > "$TMPDIR/note.go"; dats/checks/short-identifiers.sh "$TMPDIR/note.go"
	  exit: 0
