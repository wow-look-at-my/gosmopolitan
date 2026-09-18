# The APE binaries every build leg made run on this host: apetest over each
# origin's fizzbuzz and runtimeprobe, then the platform subsets. The test
# job downloads them to binaries/ape-binary-<origin> first. with-deadline.sh
# kills the process group at its deadline, which a runner timeout cannot.
tests:
	- desc: the Linux-origin binaries pass apetest here
	  cmd: export GOCACHE="$TMPDIR/gocache"; cd testdata/ape/apetest && FIZZBUZZ_BIN="$PWD/../../../binaries/ape-binary-Linux/fizzbuzz.com" RUNTIMEPROBE_BIN="$PWD/../../../binaries/ape-binary-Linux/runtimeprobe.com" sh ./with-deadline.sh 540 go test -v ./...
	  exit: 0

	- desc: the macOS-origin binaries pass apetest here
	  cmd: export GOCACHE="$TMPDIR/gocache"; cd testdata/ape/apetest && FIZZBUZZ_BIN="$PWD/../../../binaries/ape-binary-macOS/fizzbuzz.com" RUNTIMEPROBE_BIN="$PWD/../../../binaries/ape-binary-macOS/runtimeprobe.com" sh ./with-deadline.sh 540 go test -v ./...
	  exit: 0

	- desc: the Windows-origin binaries pass apetest here
	  cmd: export GOCACHE="$TMPDIR/gocache"; cd testdata/ape/apetest && FIZZBUZZ_BIN="$PWD/../../../binaries/ape-binary-Windows/fizzbuzz.com" RUNTIMEPROBE_BIN="$PWD/../../../binaries/ape-binary-Windows/runtimeprobe.com" sh ./with-deadline.sh 540 go test -v ./...
	  exit: 0

	- desc: the platform-subset APEs boot and run here
	  cmd: export GOCACHE="$TMPDIR/gocache"; cd testdata/ape/apetest && bins="$PWD/../../../binaries/ape-binary-Linux" && for sub in "tri linux/amd64,darwin/arm64,windows/amd64" "amd linux/amd64,windows/amd64"; do set -- $sub; FIZZBUZZ_BIN="$bins/fizzbuzz-$1.com" RUNTIMEPROBE_BIN="$bins/runtimeprobe-$1.com" SLIM_BIN="$bins/fizzbuzz-$1.com" SLIM_PLATFORMS="$2" FAT_BIN="$bins/fizzbuzz.com" sh ./with-deadline.sh 540 go test -v -run 'Fizz|Buzz|Number|Large|Mixed|Error|RuntimeProbe|Slim' ./... || exit 1; done
	  exit: 0
