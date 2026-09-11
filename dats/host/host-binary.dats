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

	# run.bat reports these two packages as a bare "exit status 2". The
	# boot trace build says how far the test binary gets.
	- desc: the archive/tar tests pass on this host
	  cmd: set -o pipefail; export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go test -count=1 -tags cosmontdebug -v archive/tar 2>&1 | tail -40
	  outputs:
		stdout:
			- "PASS"
	  exit: 0

	- desc: the debug/dwarf tests pass on this host
	  cmd: set -o pipefail; export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go test -count=1 -tags cosmontdebug -v debug/dwarf 2>&1 | tail -40
	  outputs:
		stdout:
			- "PASS"
	  exit: 0

	# The same package under the distribution's own runner, verbose. go test
	# from a shell passes it; run.bat reports a bare exit status 2.
	- desc: archive/tar passes under dist test on this host
	  cmd: cd src && if [ -f ../bin/go.exe ]; then GOFLAGS=-tags=cosmontdebug ./run.bat -v -run='^go_test:archive/tar$'; else GOFLAGS=-tags=cosmontdebug ./run.bash -v -run='^go_test:archive/tar$'; fi
	  exit: 0
