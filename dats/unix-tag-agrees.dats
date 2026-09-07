# cmd/dist copies internal/syslist's "unix" GOOS set, because it builds
# against the bootstrap toolchain and cannot import it. This keeps the copy
# honest: cosmo was in one list and not the other.
tests:
	- desc: cmd/dist and internal/syslist name the same unix GOOS set
	  cmd: dats/unix-tag-agrees.sh .
	  exit: 0

	- desc: the guard refuses a list that drifted
	  cmd: mkdir -p "$TMPDIR/d/src/cmd/dist" "$TMPDIR/d/src/internal/syslist" && printf 'var unixOS = map[string]bool{\n\t"linux": true,\n}\n' > "$TMPDIR/d/src/cmd/dist/build.go" && printf 'var UnixOS = map[string]bool{\n\t"cosmo": true,\n\t"linux": true,\n}\n' > "$TMPDIR/d/src/internal/syslist/syslist.go"; dats/unix-tag-agrees.sh "$TMPDIR/d"; test $? -eq 2
	  exit: 0

	- desc: the guard refuses a map it cannot find
	  cmd: mkdir -p "$TMPDIR/e/src/cmd/dist" "$TMPDIR/e/src/internal/syslist" && printf 'package main\n' > "$TMPDIR/e/src/cmd/dist/build.go" && printf 'package syslist\n' > "$TMPDIR/e/src/internal/syslist/syslist.go"; dats/unix-tag-agrees.sh "$TMPDIR/e"; test $? -eq 2
	  exit: 0
