# The APE acceptance suite, run against a binary built on each host.
#
# ONE APE runs everywhere -- that is the whole claim -- so what varies here is
# not the artifact but the machine that BUILT it: a fat APE produced on Linux,
# on macOS and on Windows must behave identically on the machine running this.
# Hence one test per build origin, all driving the same suite.
#
# This replaces three near-identical inline `run:` blocks in cosmo-ci.yml, each
# juggling an `rc` variable, a log file and a long env prefix by hand. What was
# load-bearing there is kept, and is now stated once per test instead of copied:
#
#   - with-deadline.sh wraps `go test`, because a wedged step on the macOS
#     runners has been observed to survive both the runner's own timeout and a
#     process-group SIGKILL. The wrapper abandons such a corpse so the command
#     still ends.
#   - Output goes through a FILE that is cat'ed afterwards, so an abandoned
#     descendant cannot hold the log pipe open.
#
# The workflow exports APETEST_<ORIGIN>_BINDIR for each downloaded artifact
# tree, plus APETEST_DEADLINE. A missing directory fails the test rather than
# skipping it: an origin silently not exercised is the failure this job exists
# to prevent.
#
# Note what these cases do NOT assert directly: that the kernel will load the
# binary. Every invocation here is `go test`, and the suite it drives reaches
# the APE through /bin/sh. The execve contract itself -- refused as shipped,
# accepted once the prologue has assimilated the file, or routed through a
# compiled loader on arm64 macOS -- is apetest's own execve_test.go, which
# these three cases pick up per build origin like the rest of the suite.

sandbox: false

setup:
  - test -x testdata/ape/apetest/with-deadline.sh

tests:
  - desc: the APE built on ubuntu passes the acceptance suite [origin=ubuntu]
    cmd: 'cd testdata/ape/apetest && rc=0; FIZZBUZZ_BIN="$APETEST_UBUNTU_BINDIR/fizzbuzz.com" RUNTIMEPROBE_BIN="$APETEST_UBUNTU_BINDIR/runtimeprobe.com" sh ./with-deadline.sh "${APETEST_DEADLINE:-600}" go test -v ./... > test-output.log 2>&1 || rc=$?; cat test-output.log; exit $rc'
    exit: 0
    timeout: 30m

  - desc: the APE built on macOS passes the acceptance suite [origin=macos]
    cmd: 'cd testdata/ape/apetest && rc=0; FIZZBUZZ_BIN="$APETEST_MACOS_BINDIR/fizzbuzz.com" RUNTIMEPROBE_BIN="$APETEST_MACOS_BINDIR/runtimeprobe.com" sh ./with-deadline.sh "${APETEST_DEADLINE:-600}" go test -v ./... > test-output.log 2>&1 || rc=$?; cat test-output.log; exit $rc'
    exit: 0
    timeout: 30m

  - desc: the APE built on Windows passes the acceptance suite [origin=windows]
    cmd: 'cd testdata/ape/apetest && rc=0; FIZZBUZZ_BIN="$APETEST_WINDOWS_BINDIR/fizzbuzz.com" RUNTIMEPROBE_BIN="$APETEST_WINDOWS_BINDIR/runtimeprobe.com" sh ./with-deadline.sh "${APETEST_DEADLINE:-600}" go test -v ./... > test-output.log 2>&1 || rc=$?; cat test-output.log; exit $rc'
    exit: 0
    timeout: 30m
