# Gosmopolitan

This is an **experimental fork** of the [Go programming language](https://github.com/golang/go) that adds support for building **Actually Portable Executables (APE)** using [Cosmopolitan Libc](https://github.com/jart/cosmopolitan).

## What are APE binaries?

APE binaries are single executables that run natively on multiple operating systems—Linux, macOS, and Windows—without modification or recompilation. Build once, run anywhere. This fork's output executes on Linux, macOS, and Windows (the Windows runtime surface is still growing wave by wave; see `DEBUGGING.md`).

## Building APE Binaries

```bash
# Fat APE (default): cosmo amd64 + cosmo arm64 payloads in one binary.
# GOARCH is ignored for the output. The APE ships stripped; full debug
# info lands in two sidecar ELFs next to it (program.com.dbg for amd64,
# program.com.aarch64.elf for arm64), the cosmocc convention.
GOOS=cosmo go build -o program.com main.go

# go install produces the same fat APE + sidecars in the install directory
GOOS=cosmo go install ./cmd/program

# Keep full debug info embedded in the APE instead (no sidecars)
GOCOSMOSTRIP=0 GOOS=cosmo go build -o program.com main.go

# Opt out of the fat build (single-architecture APE for the current GOARCH)
GOCOSMOFAT=0 GOOS=cosmo GOARCH=amd64 go build -o program.com main.go
```

The resulting `.com` file runs natively on Linux, macOS, and Windows. On
Windows the same cosmo amd64 image boots through the APE's PE header via
the runtime's NT personality (no embedded second build). As of wave 2
(CI-verified by the runtimeprobe gauntlet on windows-latest) the surface
covers console programs (stdout/stderr, args, environment, exit codes),
process identity, entropy, timers, the file I/O family (open/read/write/
stat, directory listing, working directory, os.Executable, temp files),
os/exec (pipes, CreateProcessW spawn, Linux-shaped wait statuses),
TCP/UDP/unix-domain sockets with deadlines (winsock emulation plus a
WSAPoll netpoller; unix sockets ride afunix.sys), and signals (SIGSEGV
recover via VEH, os/signal delivery, async preemption, kill/wait-status
decode, console Ctrl-C -> SIGINT). Still missing on Windows:
sendmsg/recvmsg (fd passing), SIGPROF profiling, and Windows/arm64. See
`DEBUGGING.md` for the detailed ladder. Debug with the sidecars
(`gdb program.com.dbg`, or `symbol-file` against the running APE);
runtime tracebacks and pprof need no sidecar. When distributing APEs,
ship them zstd-compressed: the two arch payloads are highly redundant,
so e.g. a stdlib-heavy 12.3 MB webserver APE is ~3.6 MB after
`zstd -19 --long=27` (distribution-side only - there is no runtime
self-extraction).

## Installing a Prebuilt Toolchain (Linux amd64)

CI publishes an installable toolchain tarball to [buildhost](https://pazer.build)
on every push. Install it in seconds instead of building from source:

```bash
curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=linux&arch=amd64" | tar -xz
export PATH="$PWD/go/bin:$PATH"
go version   # go version go1.26.4cosmo.r<N> linux/amd64
```

The shipped `go.env` defaults `GOTOOLCHAIN=local`, so the fork always runs
itself - no env var needed (an explicit `GOTOOLCHAIN` setting still overrides;
releases published before 2026-07-20 shipped `auto` and still need
`GOTOOLCHAIN=local`). Remember the fork defaults to `GOOS=cosmo` - pin
`GOOS`/`GOARCH` on host-side builds. To pin an immutable release instead of
the rolling branch latest, use `?v=N` in place of `branch=master`.

## Building the Toolchain

Build from the `src/` directory. Requires a Go 1.24+ bootstrap toolchain.

```bash
cd src && ./make.bash    # Unix
cd src && make.bat       # Windows
```

## Testing

With `export PATH="$GOROOT/misc/cosmo:$PATH"`, a plain `GOOS=cosmo go test <pkg>` runs cosmo test binaries on a Linux or macOS host via the `misc/cosmo` exec wrappers (see `misc/cosmo/README.md`).

## Status

This is an experimental project. Use at your own risk.

Execution is exercised in CI on x86-64 Linux, ARM64 macOS, and x86-64
Windows (plus ARM64 Linux via qemu during development). Windows execution
is cosmo-native (NT personality in the runtime; the old embedded
windows/amd64 PE payload is gone); windows-latest CI runs the full
runtimeprobe gauntlet - file I/O, dirents, TCP/UDP/unix sockets, signals,
async preemption, os/exec - against binaries built on all three platforms
(see `DEBUGGING.md` for what is still missing). macOS Intel support
is structural so far: the Mach-O assimilation header is verified against the
XNU loader's requirements by tests, but the darwin-amd64 runtime bring-up is
incomplete and untested end to end (no Intel CI runner).

## Related Projects

- [Go](https://github.com/golang/go) - The official Go programming language repository
- [Cosmopolitan Libc](https://github.com/jart/cosmopolitan) - Build-once run-anywhere C library

## License

Unless otherwise noted, the Go source files are distributed under the BSD-style license found in the LICENSE file.

![Gopher image](https://golang.org/doc/gopher/fiveyears.jpg)
*Gopher image by [Renee French](https://reneefrench.blogspot.com/), licensed under [Creative Commons 4.0 Attribution license](https://creativecommons.org/licenses/by/4.0/).*
