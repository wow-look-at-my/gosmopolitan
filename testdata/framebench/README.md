# framebench

A frame-loop allocation benchmark for js/wasm: measures per-frame wall time of a workload that allocates ~10,000 short-lived objects of mixed sizes per frame (small.

Frame time is measured on the JS side as the wall time of each `bench_frame` export call — GC assists, GC start/termination pauses, and any.

## Build

From the repo root, with the fork's toolchain built (`cd src && ./make.bash`):

```bash
export PATH="$PWD/bin:$PATH"
cd testdata/framebench
GOOS=js GOARCH=wasm go build -o framebench.wasm .
```

## Run

Requires Node.js 18+.

```bash
# Baseline: no idle-time GC help from the host.
node bench.js framebench.wasm

# Frame-aware: after each frame, donate the leftover frame budget to
# the runtime's budgeted GC mark step (go_gc_mark_step wasm export).
node bench.js framebench.wasm --markstep
```

Flags: `--frames N` (default 2000 measured frames), `--warmup N` (default 300), `--frame-budget MS` (default 16.7), `--markstep`.

Output: one JSON line plus a human-readable summary with p50/p90/p99/max frame ms, frames over 8ms / 16.7ms, and the number of GC cycles completed during.

`results/` holds saved runs: `baseline.txt` (unmodified runtime, no `--markstep`) and `after.txt` (frame-aware GC runtime changes, with `--markstep`), both recorded on the same machine.
