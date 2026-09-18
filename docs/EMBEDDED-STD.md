# The embedded standard library

A go binary can carry its standard library inside itself. `go tool embedstd -o blob` builds std for cosmo/amd64 and cosmo/arm64 and writes one blob. The blob holds each package's compiled archive, the assembly headers of `pkg/include`, and a manifest per target. The manifest names each package, its package name, its direct imports, its build ID and its archive.

## Storing it

`go tool link -apefat ... -apeappend=blob` appends the blob past both payloads and any compact debug tail, 8-aligned, and closes the file with a trailer. `GOCOSMOAPPEND=blob` on a cosmo `go build` passes that flag to the merge. Nothing maps the blob at run time. The trailer holds a magic, the blob's offset and size, and its SHA-256. `internal/cosmo/embedded` reads the trailer of `os.Executable()` and serves each entry by name. `Verify` hashes the blob against the trailer. A tool opening an entry checks the magic, the bounds and the index only.

## Reading it in process

A tool names an entry as `self:<name>`. The compiler's importcfg carries `packagefile fmt=self:std/cosmo_arm64/fmt.a`, the linker's the same, and the assembler gets `-I self:include`. `bio.OpenAny` opens a `self:` name as a bounded section of the executable, so the archive readers work unchanged. No file is written.

## The go command with no GOROOT

The go command enters embedded mode when `GOROOT` is unset in the environment and the binary carries a blob. GOROOT then names the executable. Every std package is a leaf action that is always up to date. Its archive is the `self:` name and its build ID comes from the manifest. `go list std` answers from the manifest. A reader outside the process gets a copy in the build cache. That serves `go list -export` and the vet tool's package files.

Not supported in this mode, and refused by name: testing or vetting a std package. A `GOROOT` tree in the environment builds from source, the way the tree always has.

## Verifying it

`dats/checks/embedded-std.dats` builds the blob and links a cosmo go command carrying it. It compares that command's build of a program with the source tree's byte for byte. It also checks the manifest against `go list -deps std` and links twice for one file. It vets, tests, and asks for `go test fmt` to be refused.
