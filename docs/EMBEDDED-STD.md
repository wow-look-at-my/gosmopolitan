# The embedded standard library

A go binary can carry its standard library inside itself. `go tool embedstd -o blob` builds std for cosmo/amd64, cosmo/arm64, js/wasm and wasip1/wasm and writes one blob. The blob holds each package's compiled archive, the assembly headers of `pkg/include`, and a manifest per target. The manifest names each package, its package name, its direct imports, its build ID and its archive.

Std is built with cgo on for the cosmo targets and off for the wasm targets, which have no C compiler. The blob then carries runtime/cgo, and the go command that carries the blob builds cgo programs. That needs the cosmocc compiler of each architecture on PATH (docs/CGO.md). With a compiler missing, embedstd stops before any build and names it. Its listing tolerates a package that fails, so a blob with no runtime/cgo is refused there, not written. `-cgo=false` builds std without cgo, and that go command then refuses a cgo build by name: it embeds no runtime/cgo.

## Storing it

`go tool link -apefat ... -apeappend=blob` appends the blob past both payloads and any compact debug tail, 8-aligned, and closes the file with a trailer. `GOCOSMOAPPEND=blob` on a cosmo `go build` passes that flag to the merge. Nothing maps the blob at run time. The trailer holds a magic, the blob's offset and size, and its SHA-256. `internal/cosmo/embedded` reads the trailer of `os.Executable()` and serves each entry by name. `Verify` hashes the blob against the trailer. A tool opening an entry checks the magic, the bounds and the index only.

## Reading it in process

A tool names an entry as `self:<name>`. The compiler's importcfg carries `packagefile fmt=self:std/cosmo_arm64/fmt.a`, the linker's the same, and the assembler gets `-I self:include`. `bio.OpenAny` opens a `self:` name as a bounded section of the executable, so the archive readers work unchanged. No file is written.

## The go command with no GOROOT

The go command enters embedded mode whenever the binary carries a blob. GOROOT then names the executable. Every std package is a leaf action that is always up to date. Its archive is the `self:` name and its build ID comes from the manifest. `go list std` answers from the manifest. A reader outside the process gets a copy in the build cache. That serves `go list -export` and the vet tool's package files.

The environment does not select between the carried library and a tree. `GOROOT` from outside is ignored. A binary that ships its own standard library builds against that one. A value from outside can otherwise replace it. A go command with no blob derives its tree from its own path. See `findGOROOT` in `cmd/go/internal/cfg`.

Not supported in this mode, and refused by name: testing or vetting a std package.

## Verifying it

`dats/checks/embedded-std.dats` builds the blob and links a cosmo go command carrying it. It compares that command's build of a program with the source tree's byte for byte. It also checks the manifest against `go list -deps std` and links twice for one file. It builds a js/wasm and a wasip1/wasm program through that command and compares each with the source tree's build. It vets, tests, and asks for `go test fmt` to be refused.
