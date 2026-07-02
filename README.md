# Gosmopolitan

This is an **experimental fork** of the [Go programming language](https://github.com/golang/go) that adds support for building **Actually Portable Executables (APE)** using [Cosmopolitan Libc](https://github.com/jart/cosmopolitan).

## What are APE binaries?

APE binaries are single executables that run natively on multiple operating systems—Linux, macOS, and Windows—without modification or recompilation. Build once, run anywhere.

## Building APE Binaries

```bash
# Fat APE (default): cosmo amd64 + cosmo arm64 + native windows/amd64
# payloads in one binary. GOARCH is ignored for the output.
GOOS=cosmo go build -o program.com main.go

# Opt out of the fat build (single-architecture APE for the current GOARCH)
GOCOSMOFAT=0 GOOS=cosmo GOARCH=amd64 go build -o program.com main.go
```

The resulting `.com` file runs natively on Linux, macOS, and Windows.

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

## Related Projects

- [Go](https://github.com/golang/go) - The official Go programming language repository
- [Cosmopolitan Libc](https://github.com/jart/cosmopolitan) - Build-once run-anywhere C library

## License

Unless otherwise noted, the Go source files are distributed under the BSD-style license found in the LICENSE file.

![Gopher image](https://golang.org/doc/gopher/fiveyears.jpg)
*Gopher image by [Renee French](https://reneefrench.blogspot.com/), licensed under [Creative Commons 4.0 Attribution license](https://creativecommons.org/licenses/by/4.0/).*
