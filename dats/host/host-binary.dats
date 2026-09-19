# A cosmo binary built here runs here: fizzbuzz fat and thin, the boot
# trace, an argv echo, and a test binary in the three shapes go test
# starts it in. run.bat reports a bare "exit status 2" per package; one
# binary run by hand says why.
#
# The toolchain must already be built: run make.bash first, or let the
# CI build leg do it.
tests:
	- desc: a fat fizzbuzz runs on this host
	  cmd: export GOCACHE="$TMPDIR/gocache" PATH="$PWD/bin:$PATH"; GOOS=cosmo go build -o "$TMPDIR/fb1.com" testdata/fizzbuzz/fizzbuzz.go && "$TMPDIR/fb1.com" 10 5
	  outputs:
		stdout:
			- "fizzbuzz"
	  exit: 0

	- desc: a thin fizzbuzz runs on this host
	  cmd: export GOCACHE="$TMPDIR/gocache" PATH="$PWD/bin:$PATH"; GOCOSMOFAT=0 GOOS=cosmo go build -o "$TMPDIR/fb0.com" testdata/fizzbuzz/fizzbuzz.go && "$TMPDIR/fb0.com" 10 5
	  outputs:
		stdout:
			- "fizzbuzz"
	  exit: 0

	# On NT the last line is the ntExit milestone, after the program's own output.
	- desc: the boot trace build runs and prints its milestones
	  cmd: export GOCACHE="$TMPDIR/gocache" PATH="$PWD/bin:$PATH"; GOOS=cosmo go build -tags cosmontdebug -o "$TMPDIR/fbtrace.com" testdata/fizzbuzz/fizzbuzz.go && "$TMPDIR/fbtrace.com" 10 5 2>&1
	  outputs:
		stdout:
			- "fizzbuzz"
	  exit: 0

	- desc: a child keeps its arguments
	  cmd: export GOCACHE="$TMPDIR/gocache" PATH="$PWD/bin:$PATH"; GOOS=cosmo go build -o "$TMPDIR/argvecho.com" ./testdata/argvecho/main.go && "$TMPDIR/argvecho.com"
	  exit: 0

	- desc: a test binary runs with no arguments, lists, and runs one test
	  cmd: export GOCACHE="$TMPDIR/gocache" PATH="$PWD/bin:$PATH"; GOOS=cosmo go test -c -o "$TMPDIR/strings.test" strings && "$TMPDIR/strings.test" >/dev/null && "$TMPDIR/strings.test" -test.list=. | grep -q TestLastIndexByte && "$TMPDIR/strings.test" -test.run=TestLastIndexByte -test.v | grep -q '^PASS'
	  exit: 0

	# os/exec's own tests copy their test binary and run the copy against a
	# PATH they narrowed to nothing, and this fork links several packages'
	# tests into one binary, so a package with no cgo of its own still rides
	# in a binary that carries cgo. The probe is built the same way: for the
	# host, with cgo in it. A binary that reaches its C runtime only through
	# PATH dies at load once a test takes PATH away, and the loader names
	# what it wanted here instead of the suite reporting a bare status
	# against every package that spawns a child that way.
	- desc: a copy of a host cgo binary runs with an empty PATH
	  cmd: |
		set -e
		export GOCACHE="$TMPDIR/gocache" PATH="$PWD/bin:$PATH"
		export GOOS=$(go env GOHOSTOS) GOARCH=$(go env GOHOSTARCH) CGO_ENABLED=1
		exe=$(go env GOEXE)
		printf 'package main\n\nimport "C"\nimport "fmt"\n\nfunc main() { fmt.Println("carried") }\n' > "$TMPDIR/probe.go"
		go build -o "$TMPDIR/probe$exe" "$TMPDIR/probe.go"
		cp "$TMPDIR/probe$exe" "$TMPDIR/copy$exe"
		cd "$TMPDIR"
		PATH= "./copy$exe"
	  outputs:
		stdout:
			- "carried"
	  exit: 0
