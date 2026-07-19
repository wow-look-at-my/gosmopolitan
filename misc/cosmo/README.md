# misc/cosmo

Exec wrappers that let `go test` run GOOS=cosmo test binaries directly
on a Linux or macOS host.

When GOOS differs from the host, cmd/go looks for a program named
`go_${GOOS}_${GOARCH}_exec` on `$PATH` and uses it to run every test
binary (see `FindExecCmd` in cmd/go/internal/work/build.go). The
wrappers here know how to start an Actually Portable Executable: an
already-assimilated binary (host ELF or Mach-O) is executed directly,
while a pristine APE is launched via `/bin/sh` so its boot script can
self-assimilate it.

## Usage

```sh
export PATH="$GOROOT/misc/cosmo:$PATH"
GOOS=cosmo go test -short std          # amd64 on an x86-64 host
```

No `-exec` flag is needed; cmd/go finds the wrapper automatically.

Notes:

- On an x86-64 host only cosmo/amd64 binaries can run. Test cosmo/arm64
  the same way on an ARM64 host (or under qemu-aarch64 with binfmt).
- Build the test with `GOCOSMOFAT=0` if you want a single-architecture
  binary; fat APEs work too but build both architectures first.
- Tests that fork/exec freshly built pristine APEs directly (not via
  the wrapper) still fail on hosts without an APE binfmt handler; that
  is a host limitation, not a wrapper bug.
