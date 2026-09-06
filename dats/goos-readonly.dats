# GOOS and GOARCH are variables on cosmo, so the compiler cannot keep a
# package from writing to them. This does.
tests:
	- desc: nothing in the tree assigns to runtime.GOOS or runtime.GOARCH
	  cmd: dats/goos-readonly.sh .
	  exit: 0

	- desc: the guard refuses an assignment
	  cmd: mkdir -p "$TMPDIR/g/src/runtime" && printf 'package p\nfunc f() { runtime.GOOS = "x" }\n' > "$TMPDIR/g/src/x.go"; dats/goos-readonly.sh "$TMPDIR/g"; test $? -eq 2
	  exit: 0

	- desc: the guard refuses a bare assignment inside package runtime
	  cmd: mkdir -p "$TMPDIR/h/src/runtime" && printf 'package runtime\nfunc f() {\n\tGOARCH = "x"\n}\n' > "$TMPDIR/h/src/runtime/x.go"; dats/goos-readonly.sh "$TMPDIR/h"; test $? -eq 2
	  exit: 0
