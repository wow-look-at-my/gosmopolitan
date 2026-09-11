# A cosmo binary built here runs here: fizzbuzz fat and thin, the boot
# trace, an argv echo, and a test binary in the three shapes go test
# starts it in. run.bat reports a bare "exit status 2" per package; one
# binary run by hand says why.
#
# The toolchain must already be built: run make.bash first, or let the
# CI build leg do it.
tests:
	- desc: a fat fizzbuzz runs on this host
	  cmd: export PATH="$PWD/bin:$PATH"; GOOS=cosmo go build -o "$TMPDIR/fb1.com" testdata/fizzbuzz/fizzbuzz.go && "$TMPDIR/fb1.com" 10 5
	  outputs:
		stdout:
			- "fizzbuzz"
	  exit: 0

	- desc: a thin fizzbuzz runs on this host
	  cmd: export PATH="$PWD/bin:$PATH"; GOCOSMOFAT=0 GOOS=cosmo go build -o "$TMPDIR/fb0.com" testdata/fizzbuzz/fizzbuzz.go && "$TMPDIR/fb0.com" 10 5
	  outputs:
		stdout:
			- "fizzbuzz"
	  exit: 0

	# On NT the last line is the ntExit milestone, after the program's own output.
	- desc: the boot trace build runs and prints its milestones
	  cmd: export PATH="$PWD/bin:$PATH"; GOOS=cosmo go build -tags cosmontdebug -o "$TMPDIR/fbtrace.com" testdata/fizzbuzz/fizzbuzz.go && "$TMPDIR/fbtrace.com" 10 5 2>&1
	  outputs:
		stdout:
			- "fizzbuzz"
	  exit: 0

	- desc: a child keeps its arguments
	  cmd: export PATH="$PWD/bin:$PATH"; GOOS=cosmo go build -o "$TMPDIR/argvecho.com" ./testdata/argvecho/main.go && "$TMPDIR/argvecho.com"
	  exit: 0

	- desc: a test binary runs with no arguments, lists, and runs one test
	  cmd: export PATH="$PWD/bin:$PATH"; GOOS=cosmo go test -c -o "$TMPDIR/strings.test" strings && "$TMPDIR/strings.test" >/dev/null && "$TMPDIR/strings.test" -test.list=. | grep -q TestLastIndexByte && "$TMPDIR/strings.test" -test.run=TestLastIndexByte -test.v | grep -q '^PASS'
	  exit: 0
