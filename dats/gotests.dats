# The fork's own package tests, one case per invocation.
#
# These were three inline `run:` blocks in cosmo-ci.yml, each a stack of
# `go test` lines sharing a PATH export. A stack like that reports as one
# step: the first failure hides every line after it, and nothing names what
# each invocation is for. Here each is a case with a description, so a failure
# says which area broke and the rest still run.
#
# GOOS is pinned on every command. This toolchain defaults to GOOS=cosmo and
# would otherwise emit (fat) APE test binaries the host cannot exec -- except
# where the test is deliberately GOOS=cosmo code, which runs through the
# misc/cosmo exec wrappers as a thin APE that executes natively on Linux.

sandbox: false

setup:
  - test -x ./bin/go

tests:
  # --- The APE merge: the -apefat/-apestrip/-apedbg logic, and cmd/go's
  # GOCOSMOSTRIP / -ldflags strip detection. One leg is enough.
  - desc: linker APE, PE, printf and Mach-O header construction
    cmd: 'PATH="$PWD/bin:$PATH" GOOS=linux GOARCH=amd64 go test -count=1 -run "TestAPE|TestPE|TestWritePrintf|TestMacho" cmd/link/internal/ld'
    exit: 0
    timeout: 15m

  - desc: cmd/go cosmo strip, debug and fat-build argument handling
    cmd: 'PATH="$PWD/bin:$PATH" GOOS=linux GOARCH=amd64 go test -count=1 -run "TestLdflagsSpecifyStrip|TestCosmoStripEnabled|TestCosmoDebugMode|TestCosmoMergeArgsDebugMode|TestParseToolID|TestCosmoToolIDTracksToolContent|TestCosmoFatParallel|TestCosmoFatSkipOutput" cmd/go/internal/work'
    exit: 0
    timeout: 15m

  # GOOS=cosmo-only code: the darwin sendmsg/recvmsg cmsg repack, the
  # signal/wait-status translation tables, the epoll layout. The test binary
  # is a thin cosmo APE and runs natively on this host via misc/cosmo.
  - desc: the cosmo syscall package's unit tests
    cmd: 'PATH="$PWD/bin:$PWD/misc/cosmo:$PATH" GOOS=cosmo go test -count=1 internal/runtime/syscall/cosmo'
    exit: 0
    timeout: 15m

  # The Apple itimerval ABI pins and timeval translation behind the darwin
  # SIGPROF setitimer dispatch, plus the signal translation tables.
  - desc: runtime cosmo unit tests (itimerval ABI, timeval, signals)
    cmd: 'PATH="$PWD/bin:$PWD/misc/cosmo:$PATH" GOOS=cosmo go test -count=1 -run "TestCosmoXnuItimervalABI|TestCosmoTimevalTranslation|TestCosmoSig" runtime'
    exit: 0
    timeout: 15m

  # --- Fork-divergence guardrails. go/build's structural tests are what keep
  # a fork's edits honest across an upstream uprev, and both had silently gone
  # red at one point: TestVendorPackages against the zstd vendored for cosmo
  # DWARF compression, TestDependencies against internal/runtime/syscall/cosmo.
  # cmd/internal/moddeps is the matching src/ + src/cmd module/vendor check.
  - desc: go/build dependency policy and module/vendor consistency
    cmd: 'PATH="$PWD/bin:$PATH" GOOS=linux GOARCH=amd64 go test -count=1 -short go/build cmd/internal/moddeps'
    exit: 0
    timeout: 15m

  # --- Loop-aware inlining: the loop cost discount, the loop-nested call site
  # budget and the per-caller growth allowance (cmd/compile/internal/inline).
  - desc: loop-aware inlining unit tests
    cmd: 'PATH="$PWD/bin:$PATH" GOOS=linux GOARCH=amd64 go test -count=1 cmd/compile/internal/inline/...'
    exit: 0
    timeout: 15m

  # End to end: a loop-dominated function inlines, and -d=loopinline=0
  # restores the old decisions exactly.
  - desc: loop inlining end to end, and its off switch
    cmd: 'PATH="$PWD/bin:$PATH" GOOS=linux GOARCH=amd64 go test -count=1 -run "TestLoopInlining" cmd/compile/internal/test'
    exit: 0
    timeout: 15m

  # More inlining must not change what the existing expectations say does and
  # does not get inlined.
  - desc: the inlining regress tests still hold
    cmd: 'PATH="$PWD/bin:$PATH" GOOS=linux GOARCH=amd64 go test -count=1 cmd/internal/testdir -run "Test/(inline.*\.go|newinline\.go|escape.*\.go|live.*\.go)"'
    exit: 0
    timeout: 20m
