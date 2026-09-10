#!/bin/bash
# GOWASM=threads under node: the pool, thread, speedup, STW-GC, liveness and grow-atomic demos, each with its gate. Runs from the repository root with bin/ on PATH.
set -eu
export PATH="$PWD/bin:$PATH"
export PATH="$PWD/bin:$PWD/lib/wasm:$PATH"
(cd testdata/wasmthreads && GOOS=js GOARCH=wasm GOWASM=threads go build -o /tmp/wasmthreads-smoke.wasm .)
node lib/wasm/wasm_exec_node.js /tmp/wasmthreads-smoke.wasm
(cd testdata/wasmthreads && GOOS=js GOARCH=wasm GOWASM=threads go build -o /tmp/pooldemo.wasm ./pooldemo)
# pool_demo gate: a silent fatal there must fail the leg.
set -o pipefail
node testdata/wasmthreads/pool_demo.js lib/wasm /tmp/pooldemo.wasm 2>&1 | tee /tmp/pooldemo.out
grep -q "POOLDEMO: PASS" /tmp/pooldemo.out
! grep -q "fatal error" /tmp/pooldemo.out
(cd testdata/wasmthreads && GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o /tmp/threaddemo.wasm ./threaddemo)
for i in $(seq 1 10); do
  echo "=== threaddemo run $i ==="
  node lib/wasm/wasm_exec_node.js /tmp/threaddemo.wasm | tee /tmp/threaddemo.out
  grep -q "THREADDEMO: PASS" /tmp/threaddemo.out
done
# B3: multi-P demos (reduced iterations for CI).
(cd testdata/wasmthreads && GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o /tmp/speedup.wasm ./speedup)
(cd testdata/wasmthreads && GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o /tmp/stwgc.wasm ./stwgc)
(cd testdata/wasmthreads && GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o /tmp/liveness.wasm ./liveness)
# The demos print via println (stderr), so the gates capture stderr; pipefail so a nonzero node exit fails the step.
set -o pipefail
GOMAXPROCS=1 SPEEDUP_ITERS=20000000 node lib/wasm/wasm_exec_node.js /tmp/speedup.wasm 2>&1 | tee /tmp/speedup1.out
grep -q "SPEEDUP: DONE" /tmp/speedup1.out
GOMAXPROCS=4 GOWASMTHREADSPOOL=6 SPEEDUP_ITERS=20000000 node lib/wasm/wasm_exec_node.js /tmp/speedup.wasm 2>&1 | tee /tmp/speedup4.out
grep -q "SPEEDUP: DONE" /tmp/speedup4.out
[ "$(grep 'checksum =' /tmp/speedup1.out)" = "$(grep 'checksum =' /tmp/speedup4.out)" ]
for i in $(seq 1 3); do
  echo "=== stwgc run $i ==="
  GOMAXPROCS=4 STWGC_CYCLES=15 node lib/wasm/wasm_exec_node.js /tmp/stwgc.wasm 2>&1 | tee /tmp/stwgc.out
  grep -q "STWGC: PASS" /tmp/stwgc.out
done
GOMAXPROCS=4 GODEBUG=gcstoptheworld=1 STWGC_CYCLES=8 node lib/wasm/wasm_exec_node.js /tmp/stwgc.wasm 2>&1 | tee /tmp/stwgc-stw.out
grep -q "STWGC: PASS" /tmp/stwgc-stw.out
GOMAXPROCS=4 LIVENESS_BUSY_MS=1200 node lib/wasm/wasm_exec_node.js /tmp/liveness.wasm 2>&1 | tee /tmp/liveness.out
grep -q "LIVENESS: PASS" /tmp/liveness.out
# Cross-thread grow-observation gate -- see docs/CI.md "wasm job".
(cd testdata/wasmthreads && GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o /tmp/growatomic.wasm ./growatomic)
GOMAXPROCS=4 node lib/wasm/wasm_exec_node.js /tmp/growatomic.wasm 2>&1 | tee /tmp/growatomic.out
grep -q "GROWATOMIC: PASS" /tmp/growatomic.out
GOOS=js GOARCH=wasm GOWASM=threads go test -short -count=1 sync sync/atomic internal/runtime/atomic runtime
if (cd testdata/wasmthreads && GOOS=wasip1 GOARCH=wasm GOWASM=threads go build -o /dev/null .) 2>/tmp/wasip1-threads.err; then
  echo "expected GOOS=wasip1 GOWASM=threads to be rejected" >&2
  exit 1
fi
grep -q "GOWASM=threads is only supported on GOOS=js" /tmp/wasip1-threads.err
