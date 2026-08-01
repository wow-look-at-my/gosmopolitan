# `go run` on a shebang-headed .go file, and that file executed directly.
#
# This is the fork's Go-script support, unrelated to how an APE is headed: a
# .go file whose first line is `#!/usr/bin/env -S go run` must both compile
# through `go run` and be spawnable by the kernel's script loader, which is
# what makes it a script at all.
#
# These were inline `run:` blocks in cosmo-ci.yml -- a heredoc writing a
# fixture into /tmp, then two invocations whose only assertion was the step's
# exit code. As a suite the fixture is declared, the expected output is
# asserted rather than eyeballed, and it runs identically on a laptop.
#
# GOOS/GOARCH are pinned to the host on every command: this toolchain defaults
# to GOOS=cosmo and would otherwise emit a (fat) APE the host cannot exec.

sandbox: false

setup:
  # The built toolchain, not whatever `go` the runner had first.
  - test -x ./bin/go

tests:
  - desc: go run compiles and runs a shebang-headed script
    cmd: 'PATH="$PWD/bin:$PATH"; export GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)"; go run "{inputs.hello.go}"'
    exit: 0
    timeout: 5m
    outputs:
      stdout:
        - "Hello from shebang Go script!"
    inputs:
      files:
        hello.go: |
          #!/usr/bin/env -S go run

          package main

          import "fmt"

          func main() {
              fmt.Println("Hello from shebang Go script!")
          }

  # The point of the shebang line: the kernel runs the file, no `go` on the
  # command line. `env -S` is what splits the multi-word interpreter on Linux.
  - desc: the script runs when spawned directly
    cmd: 'PATH="$PWD/bin:$PATH"; export GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)"; d="$(mktemp -d)"; cp "{inputs.hello.go}" "$d/hello.go"; chmod +x "$d/hello.go"; "$d/hello.go"'
    exit: 0
    timeout: 5m
    outputs:
      stdout:
        - "Hello from shebang Go script!"
    inputs:
      files:
        hello.go: |
          #!/usr/bin/env -S go run

          package main

          import "fmt"

          func main() {
              fmt.Println("Hello from shebang Go script!")
          }

  # A .go file WITHOUT the shebang line still has to compile: the scanner
  # change must skip a `#!` first line, not require one.
  - desc: an ordinary .go file is unaffected
    cmd: 'PATH="$PWD/bin:$PATH"; export GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)"; go run "{inputs.plain.go}"'
    exit: 0
    timeout: 5m
    outputs:
      stdout:
        - "plain"
    inputs:
      files:
        plain.go: |
          package main

          import "fmt"

          func main() {
              fmt.Println("plain")
          }
