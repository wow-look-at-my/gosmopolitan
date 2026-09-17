# An APE is host-independent by construction, so the fizzbuzz and runtimeprobe
# binaries the Linux, macOS and Windows build legs made must be the same bytes.
# The test job downloads all three origins to binaries/ape-binary-<origin>, and
# this compares them. A build input that differs between the hosts shows up
# here instead of downstream.
tests:
	- desc: every APE is the same bytes whichever host built it
	  cmd: dats/test/ape-identical.sh
	  exit: 0
	  outputs:
		stdout:
			- identical across the Linux, macOS and Windows origins

	- desc: three identical origins pass the check
	  cmd: |
		d="$TMPDIR/ape-identical-same"
		rm -rf "$d"
		for o in Linux macOS Windows; do
			mkdir -p "$d/ape-binary-$o"
			for f in fizzbuzz.com runtimeprobe.com fizzbuzz-tri.com runtimeprobe-tri.com fizzbuzz-amd.com runtimeprobe-amd.com; do
				printf 'MZqFpD=%s' "$f" >"$d/ape-binary-$o/$f"
			done
		done
		dats/test/ape-identical.sh "$d"
	  exit: 0
	  outputs:
		stdout:
			- identical across the Linux, macOS and Windows origins

	- desc: one origin whose bytes drifted fails the check
	  cmd: |
		d="$TMPDIR/ape-identical-drift"
		rm -rf "$d"
		for o in Linux macOS Windows; do
			mkdir -p "$d/ape-binary-$o"
			for f in fizzbuzz.com runtimeprobe.com fizzbuzz-tri.com runtimeprobe-tri.com fizzbuzz-amd.com runtimeprobe-amd.com; do
				printf 'MZqFpD=%s' "$f" >"$d/ape-binary-$o/$f"
			done
		done
		printf 'x' >>"$d/ape-binary-Windows/runtimeprobe-tri.com"
		dats/test/ape-identical.sh "$d"
		test $? -eq 1
	  exit: 0
	  outputs:
		stderr:
			- runtimeprobe-tri.com differs between the Linux and Windows origins
			- runtimeprobe-tri.com differs between the macOS and Windows origins

	- desc: a binary missing from one origin fails the check
	  cmd: |
		d="$TMPDIR/ape-identical-missing"
		rm -rf "$d"
		for o in Linux macOS Windows; do
			mkdir -p "$d/ape-binary-$o"
			for f in fizzbuzz.com runtimeprobe.com fizzbuzz-tri.com runtimeprobe-tri.com fizzbuzz-amd.com runtimeprobe-amd.com; do
				printf 'MZqFpD=%s' "$f" >"$d/ape-binary-$o/$f"
			done
		done
		rm -f "$d/ape-binary-macOS/fizzbuzz-amd.com"
		dats/test/ape-identical.sh "$d"
		test $? -eq 1
	  exit: 0
	  outputs:
		stderr:
			- "fizzbuzz-amd.com is missing from these origins: macOS"
