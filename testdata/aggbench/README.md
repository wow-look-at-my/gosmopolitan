# aggbench

A throughput benchmark for js/wasm codegen: the page-state aggregation kernel
from a live process-memory visualizer, which turns millions of per-page state
bytes into a grid of RGBA cell texels once per frame.

It exists because the wasm dispatch-loop work (rounds 7 and 8, see
`WASM_SHORTCOMINGS.md`) quotes numbers from "a real aggregation kernel", and
those numbers should be reproducible. This is that kernel.

It is a good measure of the backend because it is nothing but the shapes wasm
codegen is judged on -- a tight scan loop over a few megabytes, bit counting,
bounds-checked slice indexing, and a couple of hundred thousand small
branch-heavy cell computations -- and none of the things that would measure
something else instead: no allocation in the measured pass, no map or interface
traffic, no syscalls, and no JS boundary crossings.

## What it measures

- `aggregate` -- one full pass over the frame, producing every cell texel.
- `rle decode` -- expanding the run-length-encoded keyframe back into the page
  state array, the other per-frame cost.

Every pass is **checksummed**, and the checksum is printed. A toolchain that
produces a different cell buffer is wrong, not fast -- which is the failure mode
that matters when the thing under test is a compiler. The Go tests hold the
same line from the other side: the two fast paths are pinned against a naive
per-page reference implementation by a randomized parity test.

## The frame

The benchmark generates its input rather than shipping a capture (a real one is
a megabyte of binary and would date immediately). `SyntheticFrame` reproduces
the statistics that drive the kernel's cost, measured off a captured 16 GiB
target: 4.53M pages, ~3% of them non-reserved, in short runs separated by
~33-page reserved gaps, over 32 regions. It is seeded, so every run and every
toolchain sees byte-identical input.

Pass `-capture FILE` to the native binary to run against a real captured
keyframe instead.

## Build

From the repo root, with the fork's toolchain built (`cd src && ./make.bash`):

```bash
export PATH="$PWD/bin:$PATH"
cd testdata/aggbench
GOOS=js GOARCH=wasm go build -o aggbench.wasm .
```

## Run

Requires Node.js 18+.

```bash
node bench.js aggbench.wasm
node bench.js aggbench.wasm --mark          # with a mark snapshot set
node bench.js aggbench.wasm --json
```

Flags: `--iters N` (default 40), `--warmup N` (default 5), `--cells N`,
`--pages-per-cell N`, `--mark`, `--json`, `--exec PATH` (wasm_exec.js; defaults
to `$GOROOT/lib/wasm/wasm_exec.js`).

For the native ceiling, and for iterating on the kernel itself:

```bash
go build -o aggbench . && ./aggbench
go test ./...                                  # parity + unit tests
go test -run '^$' -bench . -benchtime 100x     # per-phase Go benchmarks
```

## Results

`results/` holds runs recorded on one machine, same frame, same kernel, so the
only variable is the compiler:

| | aggregate (p50) | rle decode (p50) |
|---|---|---|
| native amd64 | 2.6 ms | 5.6 ms |
| js/wasm, before rounds 7-8 | 8.4 ms | 23.3 ms |
| js/wasm, after rounds 7-8 | 4.6 ms | 11.0 ms |

The dispatch-loop work is worth 1.8x on the aggregation and 2.1x on the decode,
and takes wasm from 3.2x native to 1.8x on this kernel.

## Layout

- `frame.go` -- the generated frame, and the parser for a real capture.
- `kernel.go` -- the naive per-page reference, plus the shared types.
- `fastagg.go` -- the two fast paths (word and run) the benchmark actually runs.
- `main_wasm.go` / `main_native.go` -- the two entry points.
- `bench.js` -- the node driver.
