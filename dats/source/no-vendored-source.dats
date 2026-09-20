# A vendor path holds a git submodule, never files this repo tracks. Copied
# source writes another repository down a second time, under a path this one
# then owns. x/sys and x/text were each written down twice over, once per
# vendor tree, before the trees moved to submodules.
tests:
	- desc: no dependency source is copied into either vendor tree
	  cmd: dats/source/no-vendored-source.sh .
	  exit: 0

	- desc: the guard refuses a tracked source file under a vendor tree
	  cmd: |
		mkdir -p "$TMPDIR/copied/src/cmd/vendor/example.com/dep"
		printf '# example.com/dep v1.0.0\n## explicit\nexample.com/dep\n' > "$TMPDIR/copied/src/cmd/vendor/modules.txt"
		printf 'package dep\n' > "$TMPDIR/copied/src/cmd/vendor/example.com/dep/dep.go"
		git -C "$TMPDIR/copied" init -q . && git -C "$TMPDIR/copied" add -A
		rc=0; dats/source/no-vendored-source.sh "$TMPDIR/copied" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard refuses a module that names no submodule
	  cmd: |
		mkdir -p "$TMPDIR/nolink/src/cmd/vendor"
		printf '# example.com/dep v1.0.0\n## explicit\nexample.com/dep\n' > "$TMPDIR/nolink/src/cmd/vendor/modules.txt"
		git -C "$TMPDIR/nolink" init -q . && git -C "$TMPDIR/nolink" add -A
		rc=0; dats/source/no-vendored-source.sh "$TMPDIR/nolink" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: a module rooted in a subdirectory of its repository is covered
	  cmd: dats/source/no-vendored-source.sh .
	  exit: 0
