# The GOOS=cosmo package tests, run on this host through the misc/cosmo exec
# wrappers. They live here rather than in the workflow because each one names
# the tests it covers. A name list is a test-selection decision: a workflow
# step is a scheduler, not a place to keep one.
tests:
	- desc: the cosmo syscall shim package
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go test -count=1 internal/runtime/syscall/cosmo
	  outputs:
		stdout:
			- "ok  \tinternal/runtime/syscall/cosmo"

	- desc: the runtime package
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go test -count=1 runtime
	  outputs:
		stdout:
			- "ok  \truntime"

	- desc: the syscall package
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go test -count=1 syscall
	  outputs:
		stdout:
			- "ok  \tsyscall"

	# One cosmo binary starting another. A kernel refuses the APE header.
	# This is the whole exec surface: t.Fork, t.Setenv, t.Chdir and every
	# test that runs a helper process. The target is built into $TMPDIR
	# and never run first. This is because an APE that has run once has
	# assimilated itself on a Linux host and execs natively after that.
	- desc: a cosmo binary can exec a pristine APE
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOCOSMOFAT=0 GOOS=cosmo go build -o "$TMPDIR/fizzbuzz.com" ./testdata/fizzbuzz/fizzbuzz.go; GO_TEST_APE_TARGET="$TMPDIR/fizzbuzz.com" GO_TEST_APE_ARGS="10 5" GO_TEST_APE_WANT=fizzbuzz GOOS=cosmo go test -count=1 -run TestAPEExec syscall
	  outputs:
		stdout:
			- "ok  \tsyscall"

	# The boot script runs uname, stat and cp, and puts the standard
	# directories on PATH to find them. What the program is left holding
	# must still be the caller's own PATH: net/http/cgi hands a child
	# PATH=/wibble and reads it back.
	- desc: the boot script hands the program the caller's own PATH
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go build -o "$TMPDIR/printpath.com" ./testdata/printpath/main.go && test "$(env -i PATH=/wibble "$TMPDIR/printpath.com")" = /wibble
	  exit: 0

	# A child that loses its arguments takes down every forking test: it
	# ignores -test.run, runs the whole package, and each test in it forks
	# again.
	- desc: a cosmo child receives its arguments
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go build -o "$TMPDIR/argvecho.com" ./testdata/argvecho/main.go && "$TMPDIR/argvecho.com"
	  outputs:
		stdout:
			- "argv reached the child"
