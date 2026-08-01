# The APE heading contract, exercised through the real command line.
#
# The linker's unit tests inspect header bytes and apetest drives the built
# binary from Go; this suite is the black-box middle: `go build` as a user
# types it, then the artifact spawned the way a hook runner, a language-server
# client or a shell actually spawns it. What it pins is the whole reason
# GOCOSMOSHEBANG exists -- that execve() accepts one heading and refuses the
# other -- in the terms a person hits it in.
#
# python3 is the spawner, deliberately: it execve()s the path and reports
# ENOEXEC. A shell cannot show the failure at all, because /bin/sh (and
# execvp(3), so `env` too) FALLS BACK to running an ENOEXEC file as a shell
# script -- which is exactly why the problem is invisible until something
# spawns the binary directly, and why it looked like a client bug when it did.
#
# Unix only: the shebang heading has no Windows story by construction (see
# DEBUGGING.md "GOCOSMOSHEBANG"), so cosmo-ci runs this on the linux and macOS
# legs and not on windows-latest.

# The commands need the host: the fork toolchain under ./bin, the repo's
# testdata, and a real execve. Nothing here writes outside the temp dirs dats
# provides.
sandbox: false

shared:
  files:
    # The two builds land beside this; shared/ needs one declared file.
    README: |
      fizzbuzz APEs built once by setup, in both headings.

setup:
  - GOOS=cosmo ./bin/go build -o {shared.mz.com} testdata/fizzbuzz/fizzbuzz.go
  - GOCOSMOSHEBANG=1 GOOS=cosmo ./bin/go build -o {shared.shebang.com} testdata/fizzbuzz/fizzbuzz.go

tests:
  - desc: the default build heads the APE with the MZ magic
    cmd: head -c 8 {shared.mz.com}
    timeout: 30s
    outputs:
      stdout:
        - "MZqFpD='"

  - desc: GOCOSMOSHEBANG=1 heads the APE with a shebang instead
    cmd: head -c 9 {shared.shebang.com}
    timeout: 30s
    outputs:
      stdout:
        - "#!/bin/sh"
      "!stdout":
        - "MZ"

  # The claim, at the command line: no shell, no wrapper, no launcher script.
  - desc: execve spawns a shebang APE directly
    cmd: 'cp {shared.shebang.com} {outputs.app} && chmod +x {outputs.app} && python3 -c "import os,sys; os.execv(sys.argv[1], sys.argv[1:])" {outputs.app} 10 5'
    exit: 0
    timeout: 60s
    outputs:
      stdout:
        - "fizzbuzz"

  # The negative control that gives the mode its reason. If this ever starts
  # passing, execve has learned the APE format and GOCOSMOSHEBANG can go.
  - desc: execve refuses the default MZ heading
    cmd: 'cp {shared.mz.com} {outputs.app} && chmod +x {outputs.app} && python3 -c "import os,sys; os.execv(sys.argv[1], sys.argv[1:])" {outputs.app} 10 5'
    exit: 1
    timeout: 60s
    outputs:
      stderr:
        - "Exec format error"

  # ...and why that failure hides: a shell runs the very same file fine, so
  # the binary looks healthy right up until something spawns it for real.
  - desc: a shell still runs the default MZ heading
    cmd: 'cp {shared.mz.com} {outputs.app} && chmod +x {outputs.app} && /bin/sh {outputs.app} 10 5'
    exit: 0
    timeout: 60s
    outputs:
      stdout:
        - "fizzbuzz"

  # Running it changes the file -- on Linux the prologue writes the real ELF
  # header over its own head -- so the property that has to hold is that a
  # second, direct spawn still works. What the head becomes is per-host (macOS
  # ARM64 boots through the compiled loader and stays a script), so the
  # ELF-magic assertion lives in apetest, which can branch on GOOS.
  - desc: a shebang APE still spawns after its first run
    cmd: 'cp {shared.shebang.com} {outputs.app} && chmod +x {outputs.app} && {outputs.app} 1 2 >/dev/null && python3 -c "import os,sys; os.execv(sys.argv[1], sys.argv[1:])" {outputs.app} 10 5'
    exit: 0
    timeout: 60s
    outputs:
      stdout:
        - "fizzbuzz"
