# Fat APE build: parallel siblings, stripping, and debug tiers

How `GOOS=cosmo go build` turns two per-architecture builds into one APE, what it strips out of the shipped image, and where the debug information goes. The knobs themselves are listed in CLAUDE.md under "Building Cosmopolitan Binaries". This file is the depth behind them.

## Parallel sibling build

Parallel sibling build: the sibling-architecture build runs concurrently with the primary one. The two share no ordering constraint, so overlapping them reclaims each build's serial tail. A build whose package graph already saturates the CPU gains nothing. `GOCOSMOFATSEQ=1` forces the sequential build, which halves a fat build's peak memory because the two link phases no longer overlap. The sibling's output is buffered and replayed after the primary build's, so concurrent diagnostics never interleave. `base.AtExit` kills the child and removes its scratch directory, because the primary build can fail while the sibling still runs. Implementation: `cosmoSibling` in `src/cmd/go/internal/work/cosmofat.go`.

## Strip-and-sidecar default

Strip-and-sidecar default (2026-07-18): the fat merge embeds only each payload's loadable span - the file range its program headers reference, exactly what cosmocc's apelink ships. Naming is exact output name plus suffix: bare `go build` of package `web` gives `web`, `web.dbg`, `web.aarch64.elf`, like cosmocc's `hello`/`hello.dbg`/`hello.aarch64.elf`. `GOCOSMOSTRIP=0` (or `off`, parsed like GOCOSMOFAT) restores full embedded payloads with no sidecars. An explicit `-s` or `-w` in `-ldflags` also suppresses sidecars and embeds the user-stripped payloads as-is. Stripping does not affect runtime tracebacks or runtime/pprof (Go symbolizes via gopclntab, which lives in a loaded segment). The sidecars are for gdb/delve and offline tools.

## Debug tiers (GOCOSMODEBUG)

`GOCOSMODEBUG` selects what the sidecars carry.

`slim` makes each sidecar a debug-only ELF: the linker drops alloc contents to NOBITS and keeps the symtab and all DWARF. The file is not runnable, and gdb and delve read it unchanged.

`min` also generates less DWARF, through injected gcflags: no location lists and no inline records. Breakpoints and file:line backtraces stay exact, and argument and local values in a debugger do not. Per-tier gcflags fork the build cache, so the first build after a tier switch recompiles.

`compact` appends line-level debug info past the APE's load span and points the payload and boot ELF headers at it. An assimilated `.com` is then debugger-readable with no sidecar present. Variables stay sidecar territory.

Every cosmo `.debug_*` section is zstd-compressed (ELFCOMPRESS_ZSTD). A reader needs gdb 13, binutils 2.40 or Go's debug/elf 1.21 or later. Non-cosmo targets keep upstream's zlib.
