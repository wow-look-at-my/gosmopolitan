# runtime.GOOS names the HOST here. A syscall flag belongs to the build
# target, and os/removeall_at.go shipped the difference: it opened a directory
# with O_WRONLY|O_RDWR because the host was NT, which is EISDIR under cosmo.
tests:
	- desc: no runtime.GOOS predicate decides an open flag
	  cmd: dats/goos-not-syscall-flags.sh .
	  exit: 0

	- desc: the guard refuses the shape that shipped
	  cmd: mkdir -p "$TMPDIR/a/src/os" && printf 'package os\n\nfunc f() {\n\tflag := O_RDONLY\n\tif runtime.GOOS == "windows" {\n\t\tflag = O_WRONLY | O_RDWR\n\t}\n\t_ = flag\n}\n' > "$TMPDIR/a/src/os/x.go"; dats/goos-not-syscall-flags.sh "$TMPDIR/a"; test $? -eq 2
	  exit: 0

	- desc: a file cosmo never builds is out of scope, so the guard does not cry wolf
	  cmd: mkdir -p "$TMPDIR/b/src/syscall" && printf '//go:build dragonfly || netbsd\n\npackage syscall\n\nfunc f() {\n\tif runtime.GOOS == "netbsd" {\n\t\t_ = O_RDWR\n\t}\n}\n' > "$TMPDIR/b/src/syscall/x.go"; dats/goos-not-syscall-flags.sh "$TMPDIR/b"
	  exit: 0

	- desc: a filesystem-semantics predicate with no open flag near it is left alone
	  cmd: mkdir -p "$TMPDIR/c/src/os" && printf 'package os\n\nfunc f() string {\n\tif runtime.GOOS == "windows" {\n\t\treturn "\\\\"\n\t}\n\treturn "/"\n}\n' > "$TMPDIR/c/src/os/x.go"; dats/goos-not-syscall-flags.sh "$TMPDIR/c"
	  exit: 0
