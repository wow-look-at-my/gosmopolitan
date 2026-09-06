# Fat APE build: parallel siblings, stripping, and debug tiers

How `GOOS=cosmo go build` turns two per-architecture builds into one APE,
what it strips out of the shipped image, and where the debug information
goes. The knobs themselves are listed in CLAUDE.md under "Building
Cosmopolitan Binaries"; this file is the depth behind them.

## Parallel sibling build

Parallel sibling build (2026-07-26): the sibling-architecture build runs
CONCURRENTLY with the primary one. The two share no ordering constraint -
different GOARCH, different build-cache keys, different output paths - and
overlapping them reclaims each build's serial tail (cosmo links twice per
arch): runtimeprobe cold on 4 cores goes 15.4s -> 12.5s, ~19%, with user
time unchanged, and the output is byte-identical to a sequential build,
sidecars included. Builds whose package graph already saturates the CPU
(`go build std`) gain nothing, so the win is concentrated in exactly the
single-binary builds people run interactively. `GOCOSMOFATSEQ=1` (or `on`)
forces the old sequential behavior, which halves a fat build's peak memory
because the two link phases can no longer overlap - reach for it on a
memory-constrained machine, in preference to `GOCOSMOFAT=0`, which gives up
fat binaries entirely. The sibling's output is buffered and replayed after
the primary build's, so concurrent diagnostics never interleave, and a
failed primary build exits before the sibling is reported (one copy of each
error, not two). Implementation: `cosmoSibling` in
`src/cmd/go/internal/work/cosmofat.go`; the child is killed and its scratch
directory removed via `base.AtExit`, since the primary build can now fail
while it is still running.

## Strip-and-sidecar default

Strip-and-sidecar default (2026-07-18): the fat merge embeds only each
payload's loadable span - the file range its program headers reference,
exactly what cosmocc's apelink ships - and writes the pristine unstripped
per-arch linker ELFs next to the output as `<output>.dbg` (amd64) and
`<output>.aarch64.elf` (arm64), the names cosmo libc's FindDebugBinary
probes. Naming is exact output name plus suffix: bare `go build` of
package `web` gives `web`, `web.dbg`, `web.aarch64.elf`, like cosmocc's
`hello`/`hello.dbg`/`hello.aarch64.elf`. `GOCOSMOSTRIP=0` (or `off`,
parsed like GOCOSMOFAT) restores full embedded payloads with no sidecars;
an explicit `-s` or `-w` in `-ldflags` also suppresses sidecars and embeds
the user-stripped payloads as-is. Stripping does not affect runtime
tracebacks or runtime/pprof (Go symbolizes via gopclntab, which lives in a
loaded segment); the sidecars are for gdb/delve and offline tools - see
DEBUGGING.md "debug sidecars" (2026-07-18).

## Debug tiers (GOCOSMODEBUG)

Debug tiers (2026-07-19 + round 2 2026-07-20, GOCOSMODEBUG): `slim`
swaps the sidecars for debug-only ELFs (in-linker objcopy
--only-keep-debug: alloc contents dropped to NOBITS, symtab + all DWARF
kept, not runnable, ~-68% - runtimeprobe sidecar pair 7,818,173 ->
2,418,888 B); `min` is slim's sidecar shape plus generation-time DWARF
trims cmd/go injects into every cosmo compile (-dwarflocationlists=false
-gendwarfinl=0; explicit user -gcflags override them): sidecars shrink
another ~-38% (rp pair 1,502,680 B) at the cost of debugger
argument/local values (garbage or <optimized out>; file:line
backtraces, runtime tracebacks, and pprof stay exact - "backtraces yes,
variables no"; per-tier gcflags also fork the build cache, so the first
build after switching tiers recompiles); `compact` appends line-level
debug info (symtab + DWARF info/abbrev/line/rnglists/addr/frame;
loclists dropped) past the APE's load span and points the payload +
boot ELF headers at it, so the assimilated `.com` is debugger-readable
with no sidecar (runtimeprobe 5,517,216 -> 7,539,064 B, +38%; args show
<optimized out> - variables are sidecar territory). Since round 2 all
cosmo `.debug_*` sections are zstd-compressed (ELFCOMPRESS_ZSTD,
in-linker klauspost encoder, -13..-16% of stored DWARF vs the old
zlib): readers need gdb >= 13 / binutils >= 2.40 / Go debug/elf >= 1.21
(delve reads it; verified live with gdb 15.1 + dlv 1.27). Non-cosmo
targets keep upstream zlib. Full numbers, gdb/delve recipes, and
gotchas: DEBUGGING.md "GOCOSMODEBUG" (2026-07-19) and "debug round 2"
(2026-07-20).
