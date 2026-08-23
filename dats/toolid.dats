# The cross-build cache-poisoning guard.
#
# cmd/go derives a tool ID from the tool's own content, but only when the tool
# reports a buildID under -V=full. A fork tool that answers with the bare
# version line makes two DIFFERENT fork builds share one tool ID, so a warm
# build cache serves objects built by the other one -- binaries then SIGSEGV at
# startup, which is how the 2026-07-20 go-toolchain consumer core-dumped.
#
# This was an inline `case` statement in cosmo-ci.yml. It is a property of the
# built command line, so it belongs with the other command-line contracts.

setup:
	- test -x ./bin/go

tests:
	- desc: compile -V=full reports a content-derived build ID
	  cmd: './bin/go tool compile -V=full'
	  exit: 0
	  timeout: 2m
	  outputs:
		# The shape cmd/go's parseToolID accepts: a buildID field carrying a
		# content hash, not just "compile version go1.x".
		stdout:
			- "buildID="
		"!stdout":
			- "buildID=\n"

	- desc: every fork tool answers -V=full the same way [tool=link]
	  cmd: './bin/go tool link -V=full'
	  exit: 0
	  timeout: 2m
	  outputs:
		stdout:
			- "buildID="

	- desc: every fork tool answers -V=full the same way [tool=asm]
	  cmd: './bin/go tool asm -V=full'
	  exit: 0
	  timeout: 2m
	  outputs:
		stdout:
			- "buildID="
