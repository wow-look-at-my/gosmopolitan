# A release is named for $GOROOT/VERSION, and a go command that switches to a
# toolchain fatals when the binary it execs reports another version. So the two
# move together or a published release cannot be selected. dist stamp is what
# the publish leg runs over the toolchain its build leg built.
tests:
	- desc: dist stamp moves VERSION and the binary together
	  cmd: |
		set -eu
		src="$PWD"
		# A stamped go command has its own content ID, so every later suite in
		# this job would recompile what it links. The stamp lands on a copy.
		tmp="$(mktemp -d)"
		trap 'rm -rf "$tmp"' EXIT
		root="$tmp/goroot"
		mkdir -p "$root/pkg"
		cp -a "$src/bin" "$src/src" "$src/lib" "$src/api" "$root/"
		cp -a "$src/pkg/tool" "$src/pkg/include" "$root/pkg/"
		cp -a "$src/VERSION" "$src/go.env" "$root/"
		export GOROOT="$root" PATH="$root/bin:$PATH"
		was="$(go env GOVERSION)"
		# The version the tree was BUILT with. A stamp leaves this one alone: it
		# is what the object header and each tool's -V line carry.
		built="$("$root"/pkg/tool/*/compile -V | cut -d' ' -f3)"
		go tool dist stamp "$was.rstamptest"
		test "$(go env GOVERSION)" = "$was.rstamptest"
		test "$(go tool dist version)" = "$was.rstamptest"
		test "$("$root"/pkg/tool/*/compile -V | cut -d' ' -f3)" = "$built"
		read -r srcver < "$src/VERSION"
		test "$srcver" = "$was"
	  timeout: 20m
	  exit: 0

	- desc: dist stamp refuses a version that is not one
	  cmd: export PATH="$PWD/bin:$PATH" GOROOT="$PWD"; go tool dist stamp 1.27.0
	  exit: 2

	# -ldflags reaches the link action ID alone, so a stamp never invalidates a
	# compile the build cache already holds, and a tree that moved after the
	# build costs the one package that moved. The stamp still lands.
	#
	# Whether that package COMPILES here is not this suite's to say: the cache
	# is shared and writable, so the first run of this case publishes the
	# edited package and every later run reads it back as a hit. stampInstall
	# names what it compiles for the log, and only a private cache could assert
	# on it.
	- desc: dist stamp lands on a tree that moved under the built binaries
	  cmd: |
		set -eu
		src="$PWD"
		tmp="$(mktemp -d)"
		trap 'rm -rf "$tmp"' EXIT
		root="$tmp/goroot"
		mkdir -p "$root/pkg"
		cp -a "$src/bin" "$src/src" "$src/lib" "$src/api" "$root/"
		cp -a "$src/pkg/tool" "$src/pkg/include" "$root/pkg/"
		cp -a "$src/VERSION" "$src/go.env" "$root/"
		export GOROOT="$root" PATH="$root/bin:$PATH"
		printf '\n// the tree moves under the binaries\n' >> "$root/src/cmd/go/internal/version/version.go"
		was="$(go env GOVERSION)"
		go tool dist stamp "$was.rmoved"
		test "$(go env GOVERSION)" = "$was.rmoved"
		test "$(go tool dist version)" = "$was.rmoved"
	  timeout: 10m
	  exit: 0
