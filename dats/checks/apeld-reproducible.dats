# The APE loaders under src/cmd/link/internal/ld/apeld/bin are committed
# binaries. Every APE this toolchain links carries them, and a host that
# has no loader of its own runs one of them. So the source in that
# directory has to be what produced them, and this is what says so.
#
# zig 0.16.0, ld64.lld and llvm-strip build them. ZIG, LLD and STRIP name
# the tools. The CI job installs them.
tests:
	- desc: every committed loader rebuilds byte-for-byte from its source
	  cmd: dats/checks/apeld-reproducible.sh
	  exit: 0
	  outputs:
		stdout:
			- every loader rebuilds byte-for-byte

	- desc: a loader whose bytes drifted from its source fails the check
	  cmd: |
		cp -R src/cmd/link/internal/ld/apeld "$TMPDIR/drifted"
		printf 'x' >> "$TMPDIR/drifted/bin/apeld-linux-amd64"
		dats/checks/apeld-reproducible.sh "$TMPDIR/drifted"
		test $? -eq 1
	  exit: 0
	  outputs:
		stderr:
			- differs from what its source builds
