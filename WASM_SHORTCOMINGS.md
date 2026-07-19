# WebAssembly Port Shortcomings (GOOS=js, GOOS=wasip1)

This document catalogs the state of the two WebAssembly ports in this tree:
what this fork has fixed, what remains broken or missing, what each remaining
item would take to fix, and what it costs to use the fixes. Snapshot date:
2026-07-17 (round 5), based on the go1.26 tree this fork tracks. Severity: P0
(hang/crash/silently wrong) through P3 (polish/docs). Fixability: fork-fixable
(a bounded patch in this tree), needs-wasm-proposal (blocked on a WebAssembly
spec proposal or an upstream megaproject), or inherent (a consequence of the
platform that can only be documented).

The wasm ports were inherited unmodified from upstream Go: before the fixes
below, `git log` showed zero fork-specific commits under `src/runtime/*js*`,
`src/runtime/*wasip1*`, `src/syscall/js/`, `lib/wasm/`, or the wasm compiler
backends. Every shortcoming in this document is therefore also upstream Go's
shortcoming, with upstream issue links given where they were verified to
exist. Four audits (runtime/scheduler, syscall/js + JS glue, compiler/linker
backend, wasip1 + stdlib) produced the findings; the fixes were then made and
verified in this tree under Node.js 22 (js) and wazero 1.12 (wasip1). A second
round (2026-07-05) of codegen, runtime, and interop work built on that base,
and a third round (2026-07-05) added CPU profiling, objdump/nm/addr2line
support, synchronous stdio under node, an idle forced-GC nudge, and a
GOWASM=tailcall gate; entries are dated below where the distinction matters.

## Fixed in this fork

| Area | Problem | Upstream ref | Commit | Before -> after |
|---|---|---|---|---|
| cmd/compile, cmd/link | No DWARF was ever emitted for wasm: the compiler forced -dwarf=false and the linker skipped generation wholesale, so binaries carried zero source-level debug info; separately, the name section was written after the producers section, which made every Go wasm module unreadable by the whole llvm-* tool family | none verified | 544a8bba..c53f8dad | zero .debug_* sections -> DWARF v5 emitted as .debug_* custom sections per the WebAssembly DWARF convention (code addresses are byte offsets from the start of the code section's contents, the same base lld and Chrome DevTools use): full DIE tree plus statement-level line tables, `llvm-dwarfdump --verify` clean on both ports; on by default (~+39% file size), stripped by -ldflags=-w; bonus fix: the name section now precedes producers per the tool conventions, so llvm-* tools accept Go wasm modules at all |
| Scheduler | Nothing ever preempted a running goroutine on wasm: no sysmon, no signals, cooperative arming never fired with one M. A CPU-bound loop starved every timer, the GC, and all other goroutines forever | #60857, #71134, #10958, #36365 | aa31fde9 | tight loop hangs program -> loop backedges test a per-g guard; timers fire, GC completes, other goroutines run |
| Scheduler | GOMAXPROCS=2 in the environment (not the API) crashed at startup with "newosproc: not implemented" | none verified | c7590070 | startup throw -> clamped to 1 like the API path |
| Scheduler | Deadlock while a js.FuncOf callback is blocked printed only the generic message | #34478, #65773 | d42a1c3b | bare "all goroutines are asleep" -> two-line note explaining the frozen JS event loop |
| Scheduler | Deadlocked program that does not link syscall/js crashed with a nil-pointer panic in handleEvent when node's exit probe fired | #70869 | cc9c71c8 | nil deref inside the runtime -> proper "all goroutines are asleep - deadlock!" report |
| Scheduler | The nil-eventHandler probe event was miscounted as a blocked user callback by the new deadlock note | none (interaction of the two fixes above) | d34b72e2 | misleading js.FuncOf note on plain deadlocks -> note suppressed for the probe |
| Scheduler | timeoutEvent.diff read the global idleTimeout instead of its receiver (latent) | none verified | a5397fcc | works only for the one existing call site -> correct for any caller |
| Runtime (wasip1) | signal.Notify parked its receiver in a busy-spin notetsleepg: 100% CPU for the process lifetime and checkdead permanently masked | none verified | 04d0fcde | hot spin + silent hangs -> receiver parks; deadlock detection works again |
| Runtime (wasip1) | Hosts that stub poll_oneoff (ENOTSUP/ENOSYS) crashed the program on first scheduler idle, then again inside the crash report | #78513 | d12e4b59 | double fatal error -> poller marked dead, timers keep working by spinning |
| Runtime (js) | Fake netpoll comment claimed the poller is never used; it runs on every timer-idle cycle | n/a (comment) | f96c76bd | misleading comment -> describes the real behavior and the busy-wait it causes |
| Runtime | The periodic (2-minute) forced GC never fired: forcegchelper is resumed only by sysmon and wasm has no sysmon, so an idle program never collected after its last allocation burst; finalizers and cleanups sat forever | none verified | 5230d942 | heap parked at high-water mark forever -> findRunnable tests the time-based trigger and idle Ms cap their timer sleep at the forced-GC deadline; "GC forced" fires at ~120s on both ports, upstream TestPeriodicGC unskipped |
| syscall (wasip1) | Preopens matched by string prefix: with /data preopened, /database/x silently resolved inside /data | none verified | 0e665b44 | wrong file, silently -> match only on path component boundaries |
| syscall (wasip1) | Paths outside all preopens (or with no preopens) failed with EBADF "Bad file number", os.IsNotExist false | #63466, #60732 | b2706c78 | fd-bug-looking error -> ENOENT, os.IsNotExist true |
| time (wasip1) | time.Local was permanently UTC; $TZ ignored; no zone source worked | none verified | db2be627 | UTC forever -> standard $TZ handling via $ZONEINFO, preopened zoneinfo dirs, or time/tzdata |
| os/exec | LookPath reported "not found in $PATH" for files that exist | none verified | 55497db5 | misleading PATH chase -> "cannot execute binaries on wasip1/wasm", matches errors.ErrUnsupported (and still ErrNotFound) |
| net/http (js) | Node.js was hard-locked onto the fake network; every http.Get failed even though node >= 18 ships fetch | #57613, #60810 | a18df3b8 | no real HTTP under node -> GODEBUG=jsfetchnode=1 opt-in enables the Fetch transport |
| syscall/js | Reflect.get in valueIndex/valueLength can reenter Go (Proxy traps, accessors); results were stored through a stale SP after stack growth: memory corruption | none verified (present upstream) | 9b6483bb | Index/Length return garbage, stack clobbered -> SP refreshed like valueGet/valueCall already did |
| syscall/js | js.Error.Error() panicked when JS threw a non-object (throw "boom"), masking the original error | none verified | 866f91a9 | panic while printing panic -> falls back to the thrown value's string; also fixes Length's wrong panic text and stale package docs |
| syscall (js) | mapJSError panicked on any JS error without a known .code, killing the program from one failed fs op | none verified | 1c34c287 | whole-program crash -> unknown errors map to EIO, operation fails normally |
| syscall/js | No way to await a promise: every async JS API cost a FuncOf/then/catch/channel dance per call site | #69720, #72045, #28911 | 5b975b77 | ~20 lines of boilerplate per await -> js.Await(v): Promise.resolve semantics, blocks the goroutine, (result, nil) on fulfillment, (undefined, Error{reason}) on rejection with a total Error; in-callback misuse documented and reported by the runtime's deadlock note |
| syscall/js, lib/wasm | CopyBytesToGo/CopyBytesToJS accept only Uint8Array/Uint8ClampedArray; Float32Array & co. copied element-wise through Index/SetIndex | #32402, #25532, #38011 | 68d02283 | one boundary crossing per element -> CopyToGo/CopyToJS bulk-copy []int8/[]uint8/[]int16/[]uint16/[]int32/[]uint32/[]float32/[]float64 with strict TypedArray kind matching, one generic glue pair |
| syscall (js) | A JS fs method that throws synchronously (broken or monkey-patched fs, nonstandard host) escaped as a js.Error panic and killed the program | none verified | 11f623b2 | whole-program crash from one file op -> thrown exception mapped through mapJSError like a callback error (unknown -> EIO), *PathError wrapping preserved, non-JS panics still propagate |
| lib/wasm | Node fallbacks were broken: require("performance") is not a builtin; the crypto fallback only worked by accident on node >= 17.4 | nodejs/node#49272 (context) | d2f24bdc | crash or late TypeError on old node -> explicit node >= 18 check with a clear error |
| lib/wasm | Browser output shim: one line buffer for both fds, everything via console.log, unterminated final output lost at exit | none verified | 3fc95897 | interleaved/lost output -> per-fd buffers, stderr to console.error, flush on exit |
| lib/wasm | Browser output shim decoded each write chunk independently: a multi-byte rune split across two writes rendered as U+FFFD | none verified | 1ab406a9 | mojibake on split runes -> per-fd streaming TextDecoder (decode {stream:true}), decoder drained at exit before the partial-line flush |
| cmd/link, lib/wasm | argv+env budget was 8KB; env-heavy hosts (CI) died at startup with "exceeds limit" | #49011 | 332bb923 | startup crash under big env -> 61440-byte budget (data floor 64KB), error message reports needed/available |
| cmd/compile | go:wasmexport with 17+ small params passed the 128-byte check, then the assembler panicked (ICE "bad Get: invalid register") | none verified | 4ead4b0a | internal compiler error -> proper "too many parameters" diagnostic; 16 params still compile |
| cmd/compile | zeroRange emitted 3 instructions per 8 bytes for large stack zeroings | none verified | 41389912 | 24-instruction prologue for a 64-byte range -> single memory.fill (>32 bytes) |
| cmd/compile | The loop-preemption guard (round 1) was a 64-bit load+compare built from generic ops; the scheduler hoisted the load away from the compare, adding a register round-trip, an extend, and a wrap to every backedge | n/a (round-1 follow-up) | 5f9934ce | 9-instruction guard -> fused single-op 32-bit check (Get SP; Get g; wrap; i32.load; i32.lt_u); worst-case loop ~18% faster under wazero, unchanged under node (V8 already hoisted the disarmed-case load); hello.wasm -4.4KB |
| cmd/compile | Every sync/atomic and internal/runtime/atomic op was a cross-package call into atomic_wasm.go's plain bodies: a dozen instructions of call protocol around a one-instruction operation, on a single-threaded target | none verified | d9b36bf5 | call per atomic op -> intrinsified to plain loads/stores and inline RMW sequences; atomic add 2.0x (node) / 4.0x (wazero) faster, mixed atomic ops 4.9x / 8.0x, sync.Mutex Lock/Unlock 1.8x / 4.1x; hello.wasm -42KB |
| cmd/compile | Signed 64-bit division called the runtime.wasmDiv assembly helper on every int64 divide, only to special-case MinInt64/-1 (wasm's i64.div_s traps there) | none verified | 93f1b09b | call + branch per divide -> branchless inline form (divide by 1 when divisor is -1, negate after); 18% (node) / 23% (wazero) faster; MinInt64 and divide-by-zero semantics verified byte-identical to host Go; the now-dead runtime.wasmDiv helper removed end to end (21e24728) |
| Runtime | pprof CPU profiles were silently empty on both ports: no signals means sigprof never ran and the profiler hooks were empty stubs. On wasip1 it was worse than useless - the profile reader busy-spun in note-based reads for the whole window (5s window: ~5.3s of user CPU collecting zero samples) | none verified | ebf7b6a5 + 51acdc8b | zero samples -> the loop-preemption gate doubles as a 100Hz sampler (callers-style unwind from the backedge that hit the check); a two-function 70/30 workload profiles as 69.5/30.5 under node and 68.3/31.7 under wazero; the reader does paced non-blocking reads (5s profiled idle window on wazero: ~5.3s user CPU -> ~1.4s, about the unprofiled baseline); execution traces get CPU samples too (~200 StackSamples in a 2s trace); upstream pprof tests enabled on wasm |
| syscall (js) | Writes to stdout/stderr went through the callback-based fs.write, whose completion is delivered on the JS event loop: with any CPU-busy goroutine, fmt.Println never returned (the bytes reached the pipe but the printing goroutine parked forever) and an os.Exit right after it never ran | none verified | 27de1634 | print-under-spinner hangs the program forever -> fds 1 and 2 go through fs.writeSync with a partial-write/EAGAIN retry loop (node makes pipe stdio nonblocking; a 10MB write to a slow pipe takes ~112000 EAGAIN retries); the spinner repro now exits in 0.08s, 10MB survives byte-identical through file redirects and slow pipes, and stdout/stderr keep program order |
| Runtime (js) | The round-2 forced-GC fix deliberately skipped one case: a fully idle js program with no Go timers pending got no wake source at all (a regular setTimeout would hold node's event loop open and defeat the exit-time deadlock probe), so it still never collected after its last allocation burst | n/a (round-2 follow-up) | 105e98dd | idle heap parked at high-water mark forever -> beforeIdle arms a weak, unref'd timeout for the forced-GC deadline (new scheduleWeakTimeoutEvent glue); "GC forced" fires at ~120s while fully idle, and deadlock reporting - with and without syscall/js linked - is unchanged |
| cmd/internal/obj/wasm | Compiler-generated tail calls (embedded-method wrappers and the like) always grew the WebAssembly stack with i32.const 0; call; return - the round-2 investigation found the tail-call proposal usable on node but unshippable as a default (wazero rejects it) | n/a (round-2 follow-up) | b36ddb5b | call+return always -> GOWASM=tailcall (default off) emits return_call for the RET-to-symbol path: ~3% on a wrapper-heavy benchmark under node, default output stays byte-identical, wazero rejects tailcall binaries with its known "tail-call is disabled" error; also restores the GOWASM feature-gate mechanism (build tag + build cache key) that had decayed to an empty struct |
| cmd/objdump, cmd/internal/objfile | go tool objdump, nm, and addr2line all refused linked wasm binaries ("unrecognized object file" / "unsupported architecture") | none verified | 9b2c1f71 + fcac4977 | no binary inspection at all -> all three work on js and wasip1 modules: symbols synthesized from the code section, pclntab found by scanning the reconstructed data segment (so stripped -ldflags=-s binaries work too), the full core opcode space decodes byte-exactly (1686/1655 functions on the reference js/wasip1 binaries), call targets symbolized via pclntab or import names, Go's register globals (SP, CTXT, g, ...) annotated. Source lines are function-granularity only: PC_B counts resume points, not bytes, so per-instruction line mapping is unrecoverable |
| cmd/compile | The two-variable range-over-slice lowering carries the iteration position across the loop backedge only as a uintptr (hu), invisible to the precise stack scan by design; insertLoopReschedChecks (preemptibleloops, this fork's wasm default) inserts a call exactly there, so a goroutine parked at the backedge during mark had nothing rooting the backing array. On wasm cheapComputableIndex reports false, so every two-variable slice range used the scheme. Surfaced as a nondeterministic nil-Function deref in CI's TestProfilerStackDepth (31/380 runs failed with GOGC=1); a distilled reproducer (range over a struct-field slice while a helper goroutine runs runtime.GC, GODEBUG=clobberfree=1) failed deterministically under both wazero and node | none verified (latent upstream for GOEXPERIMENT=preemptibleloops) | c2b233dd | GC use-after-free of the array the loop is still reading -> index-based lowering (v1, v2 = hv1, ha[hv1]) whenever preemptibleloops is enabled: the slice stays live, GC-visible, and stack-copy-adjusted for the whole loop; reproducer clean on both engines, amplified TestProfilerStackDepth soaked 0 failures in 1512 runs (35 min) |
| docs | os/signal said nothing about wasm; net's fake network was described only as a testing aid in a source comment | n/a | 8ed4b658 | undocumented traps -> package docs state what works, what silently does not, and the escape hatches |
| Runtime (GC, js) | GC mark work bunched into frame-sized bursts (round 5, 2026-07-17): with one P the pacer's cons/mark runway came out at a few hundred KB, so whole cycles ran as in-frame assist bursts; idle mark drains were untimed and blocked the event loop until mark completion; and the fractional mark worker's 25% quota is measured against wall time - which on js includes host-idle time - so frame-driven apps paid ~4ms of every 16.7ms frame while marking | none verified | 2c385994 | framebench (10k mixed-size allocs/frame under node): p99 frame time 21.6ms -> 4.8ms, 535/2000 -> 0/2000 frames over 8ms. Pacer minimum runway (>= half the trigger-to-goal headroom, trigger floor lowered ~0.7 -> ~0.5) plus a cycle-start background-credit seed; idle drains bounded at 2ms (js additionally yields to the event loop with a 1ms re-arm); new `go_gc_mark_step(budgetMs) -> bool` wasm export for host-donated between-frame marking (no-op outside a cycle, returns whether work remains, runs mark termination between frames when it finishes the cycle); fractional quota capped at 5% while the host donates idle time. The pacer changes, the drain deadline, and the budgeted mark step core are platform-independent (all platforms pace and drain the same way, and TestGcPacer plus portable mark-step tests exercise the real behavior); only the event-loop yield glue and the wasm export itself are js-specific |
| net, syscall (wasip1) | WASI preview 1 defines no way to create or connect a socket, so net.Dial on wasip1 could never reach a real network: every dial and listen went to the in-process fake net | #65333, #67673 | 194bcf71 + d85a610b + 27fa0219 + 6eb4bf17 + a3a37b18 | fake network only -> GOWASI=wasmedgesock (default off; build tag wasip1.wasmedgesock; hashed into the build cache key) routes TCP through the WasmEdge socket extension (second-state SDK v0.4.3 ABI): real Dial (IP literals), Listen/Accept, deadlines, concurrency, http.Get and http.Serve end to end; verified by the testdata/wasip1sock wazero reference host (1MB echo round-trip, 8 concurrent conns, HTTP both directions, prompt ECONNREFUSED, read-deadline timeout); default builds stay byte-identical and stock runtimes reject opt-in binaries ('"sock_open" is not exported in module "wasi_snapshot_preview1"'); UDP/DNS/unix sockets stay fake |

## Remaining shortcomings

### Scheduler and runtime

- P1, inherent (design work to improve): Scheduling is still cooperative
  toward the HOST. The preemption fix lets the Go scheduler interrupt a busy
  goroutine, but Go as a whole still cannot return control to the JS event
  loop while any goroutine is runnable (`src/runtime/lock_js.go` beforeIdle/
  pause design). During a CPU burst, Go-side timers and goroutines make
  progress but JS-side events starve: rendering, setTimeout callbacks, and
  node's async fs completions wait until Go goes idle. A browser tab still
  freezes for the duration of a long computation. True time-slicing back to
  the host needs a pause/resume of a runnable world (only safe when no JS
  call is in flight) - a design project, not a patch. Since round 5 the GC
  is no longer a source of this: idle marking is deadline-bounded and
  yields to the event loop, and hosts can donate idle time between frames
  via the go_gc_mark_step export - a long user computation still freezes
  the tab.
- P1, needs wasm proposal: One thread, one P, forever. newosproc throws
  (`src/runtime/os_wasm.go:110`), NumCPU=1, atomics are plain loads/stores
  (`src/internal/runtime/atomic/atomic_wasm.go`). Parallelism needs the wasm
  threads proposal plus a large runtime port (golang/go#28631, #56305).
  Toolchain groundwork landed 2026-07-17 (threads phase B0): `GOWASM=threads`
  (default off, GOOS=js only) makes Go's atomic ops compile to the threads
  proposal's real 0xFE sequentially-consistent atomic instructions, the
  assembler/encoder knows the full 0xFE opcode space, the linker emits an
  imported shared linear memory (module `gojs`, field `mem`, limits flag
  0x03, max 2048 MiB) instead of a module-local one, and `wasm_exec.js`
  creates and supplies the matching SharedArrayBuffer-backed
  `WebAssembly.Memory` (`go.provideMemory(bytes)`; `wasm_exec_node.js`
  calls it automatically, and it is a no-op for ordinary modules). The
  runtime is still strictly single-threaded - one M, one P, no worker
  spawning - so this changes no observable behavior yet; it is the
  instruction-set and memory-model substrate the runtime port will build
  on. Node needs no flags; browsers need cross-origin isolation
  (COOP/COEP) for SharedArrayBuffer. wazero/wasmtime lack the proposal,
  so wasip1 rejects the flag at link time. Default (no GOWASM=threads)
  output is verified byte-identical.
  Phase B1 (2026-07-17) makes "one wasm instance per worker over one
  shared memory" real: under GOWASM=threads the linker emits PASSIVE
  data segments (active segments would be re-applied on every
  instantiation, so a worker instance would clobber the live heap and
  runtime state in the shared memory) plus two synthetic exports -
  `_initmem`, which applies the segments via memory.init and drops them,
  called exactly once by the MAIN instance from `Go.run` in wasm_exec.js
  (the JS-tells-instance gating model emscripten also uses; workers must
  never call it), and `wasm_probe_atomic_add(addr, delta)`, a
  runtime-state-free seq-cst i32.atomic.rmw.add that worker instances
  can call before the runtime is thread-aware. A DataCount section is
  emitted for single-pass validation. On the JS side,
  `wasm_exec_node.js` compiles a threads module once
  (WebAssembly.compile, kept on `go._module`), and
  `lib/wasm/wasm_exec_pool_node.js` (`GoWorkerPool`) pre-spawns N
  node worker_threads running `wasm_exec_worker_node.js` (a thin wrapper
  over the host-agnostic `wasm_exec_worker.js`, which documents the
  init/ready/call/result postMessage protocol and is Web Worker-ready);
  each worker instantiates the same module against the same shared
  memory with every gojs.* runtime import stubbed to throw - Go code
  cannot run on workers yet. `testdata/wasmthreads/pooldemo` +
  `pool_demo.js` (CI-run) prove the core: 4 workers hammer a shared
  counter from wasm 0xFE atomics to an exact expected sum while the main
  instance's Go heap/data checksums stay identical.
  Phase B2 (2026-07-17) makes REAL Go code run on worker threads. The
  runtime gains a futex layer over memory.atomic.wait32/notify
  (`futexsleep`/`futexwakeup` in `src/runtime/sys_wasmthreads.s`):
  under GOWASM=threads, runtime mutexes are the classic futex lock and
  notes are futex-based (`lock_jsthreads.go`), so Ms on different
  threads block and wake each other; `notetsleepg(n, -1)` parks the
  goroutine instead of blocking the M (os/signal loops, profile readers
  and the like must not pin the event-loop thread). newosproc is real:
  it hands the new M through a spawn mailbox (state/mp/seq words +
  futex) to a pool worker parked inside the new `wasm_thread_run`
  export - a raw-wasm futex wait that needs no Go state - which then
  sets its per-instance SP/g globals to the M's heap-allocated g0 stack
  and enters mstart (per-instance globals are exactly why one instance
  per worker exists). g0 stacks live in the shared heap, so cross-
  thread stack access just works. `wasm_exec_node.js` pre-spawns
  GOWASMTHREADSPOOL (default 4, 0 disables) runtime workers per
  program; a worker serves one M for the process lifetime (Ms never
  exit on wasm), parked Ms are reused by the scheduler, and newosproc
  throws after 10s if no worker claims (pool exhausted/disabled).
  Worker instances get real pure-runtime imports (wasmWrite via
  fs.writeSync, nanotime1 on the main instance's clock base, walltime,
  getRandomData, wasmExit forwarded to the main thread) so println,
  clocks and crashes work on worker Ms; syscall/js and the event-loop
  imports still throw there - JS values live on the main thread only.
  The event loop stays main-M-only: beforeIdle routes only the main M
  to the pause/resume machinery (now shared in `event_js.go`), worker
  Ms do capped timed futex sleeps for pending timers or park in stopm.
  Main-thread caveat: the main M may futex-wait (node allows it;
  browsers do not, so worker Ms are node-only), and while it waits the
  host event loop is stalled until a worker futex-wakes it. GOMAXPROCS
  stays clamped to 1: only one M runs Go at a time, handing the P
  around; the demo hook `runtime.wasmThreadsRunOnNewM` (linkname,
  `-ldflags=-checklinkname=0`) pins the calling goroutine to its M so
  the scheduler must move the P to another M (stoplockedm/handoffp/
  newosproc/startlockedm - the organic locked-M path; public
  LockOSThread remains a wasm no-op this phase). newm's template-thread
  deferral is disabled on wasm: newosproc clones no thread state, so
  spawning from a locked M is safe and no template thread exists.
  `testdata/wasmthreads/threaddemo` (CI-run 10x) shows goroutines with
  channels, sync.Mutex and shared-heap traffic on three Ms across three
  threads, nested spawns, parked-M reuse and runtime.GC over the shared
  heap; `go test -short sync sync/atomic internal/runtime/atomic
  runtime` passes under GOWASM=threads (including a new in-tree spawn
  test). Still missing (B3+): multi-P parallelism (the GOMAXPROCS
  clamp), preemptive STW of a running worker beyond cooperative
  loop/prologue checks, a non-blocking main-thread park (event loop
  currently stalls while the main M waits), syscall/js host-call
  forwarding from worker Ms, cross-thread CPU profiling, fence
  emission, and browser workers. NOTE: unlike B0/B1, default
  (non-threads) js builds are no longer byte-identical to the previous
  phase - the runtime port necessarily touches shared runtime sources
  (lock_js.go event-machinery split, newosproc/usleep hooks), which
  shifts symbols, pclntab file tables and DWARF even though the
  non-threads code paths are semantically unchanged (verified by the
  unchanged non-threads test suite and identical program output).
- P1, inherent: Blocking inside a js.FuncOf callback still deadlocks - now
  with a clear error, but the semantics cannot change: the callback runs
  synchronously on the JS thread and nothing can block there. Worse, if any
  unrelated timer exists, the deadlock is undetectable and becomes a hot spin
  (fake netpoll returns immediately; the scheduler polls until the timer is
  due, `src/runtime/netpoll_fake.go`). Upstream #26045, #34324. js.Await
  (this fork, 5b975b77) is the usability fix for ordinary goroutines; the
  callback-cannot-block constraint itself remains.
- P2, inherent: Linear memory never shrinks. wasm has no memory.shrink;
  the sbrk allocator can reuse but not return (`src/runtime/mem_sbrk.go`).
  Peak footprint persists until the instance dies (golang/go#59061, #27462).
  The background scavenger runs and accomplishes nothing.
- P2, fork-fixable: faketime is broken on wasm: on js the beforeIdle path
  makes checkdead's timejump unreachable (fake clock never advances, program
  hangs re-arming real timeouts); on wasip1 the timejump path throws
  "notesleep not supported by wasi" (`src/runtime/lock_wasip1.go:77`).
- P3, audited non-issue (2026-07-05): wasip1 notetsleepg still busy-yields
  for TIMED waits (`src/runtime/lock_wasip1.go:87-106`), but no timed
  caller is reachable on wasip1 - sysmon's is gated by haveSysmon, and the
  stop-the-world notes cannot time out with gomaxprocs==1. Since round 3
  the pprof profile reader no longer uses notes either (it does paced
  non-blocking reads), so no reachable busy-yield caller remains, timed or
  untimed.
  Other landmines: notesleep/notetsleep throw on both ports; osyield is
  UNDEF (`src/runtime/sys_wasm.s:10`); js usleep is a no-op; the g0 stack is
  a fixed 8KB global with no guard (`src/runtime/sys_wasm.go:13`).
- P3, inherent: In a browser, a fully deadlocked program is silent - there is
  no "event loop drained" signal, so only the node wrapper detects deadlock
  at exit (golang/go#32764).

### JS interop (syscall/js and the glue)

- P1, inherent (footgun, document): js.Func leaks by design. FuncOf pins the
  Go closure in a package-global map and Release() is the only reclaim path
  (`src/syscall/js/func.go`); dropping the last reference without Release
  leaks the closure, the id, and the JS wrapper forever. Plain js.Value is
  finalizer-managed and does not leak.
- P1, inherent (escape hatch fork-fixable): Lone surrogates are destroyed in
  both directions. JS strings are WTF-16; TextEncoder replaces unpaired
  surrogates with U+FFFD, so Value.String() is lossy and non-round-trippable,
  with no error and no lossless alternative (golang/go#29642 adjacent). Keys
  read from JS maps can fail to match when written back.
- P3, evaluated and deferred (2026-07-05): managing js.Value lifetimes with
  runtime.AddCleanup instead of SetFinalizer (resurrection-free semantics,
  no serialized finalizer goroutine) was implemented and measured: it costs
  two extra allocations on every JS call that returns a non-number value
  (1 -> 3 allocs/op; AddCleanup boxes its argument and allocates a generic
  closure, see the TODO in `src/runtime/mcleanup.go:180`). makeValue stays
  on SetFinalizer until upstream slims AddCleanup down; revisit then.
- P2, fork-fixable: Interop cost. Every JS->Go string is 3 import round trips
  plus 2 copies; property names are re-decoded on every Get/Set/Call; every
  non-number value crossing allocates a finalizer-tracked handle
  (golang/go#32591, #35917). No batching, no cached method handles, no
  externref.
- P2, document: int64/uint64 route through float64 - values beyond 2^53
  silently lose precision; there is no BigInt bridge. -0 is canonicalized to
  +0; NaN payloads are canonicalized (NaN-boxing requires it).
- P3, inherent: A panic inside a js.FuncOf handler kills the whole program
  (no recover, no panic-to-JS-exception translation); a handler returning an
  unsupported Go type does the same via ValueOf. Calling a wrapper after Go
  exited throws "Go program has already exited" into the JS caller.
- P3, inherent: GOOS=js binaries speak a private sp-based ABI with
  wasm_exec.js ("gojs" imports are (i32)->()); glue and toolchain must match
  versions, and no other host can run them. Embedders wanting a standard ABI
  must use wasip1.

### Compiler and linker codegen

- P2, upstream megaproject: The dispatch-loop execution model. Every function
  is (i32)->i32; every basic-block boundary is a resume point; every jump
  goes through a br_table at the function top; every call is ~13 opcodes plus
  a resume-address store to linear memory (`src/cmd/internal/obj/wasm/
  wasmobj.go:438-543`). Measured upstream at <= ~20% of native Go in the best
  case (golang/go#65440); the standing redesign discussion is #43033
  (relooper/Asyncify). Only call sites actually need to be resumable - a
  structured-control-flow lowering would remove most dispatch round trips,
  but it is a compiler megaproject.
- P1, needs wasm proposal: No threads (see above), no SIMD (no v128 anywhere
  in the backend), tail calls only behind GOWASM=tailcall (see the next
  bullet - the proposal is standardized but the default wasip1 runtime
  cannot run it), no multi-value returns (single-i32 internal ABI),
  no externref/WasmGC (golang/go#63904 - blocked by interior pointers), no
  memory64 (upstream momentum is the opposite: GOARCH=wasm32, #63131).
  GOWASM feature gating had decayed to an empty struct; round 3 (b36ddb5b)
  restored the mechanism (build tags, build cache key) for tailcall, so
  future gated features have a working template.
- P2, blocked on engine support (mechanism landed): wasm tail calls are
  implemented behind GOWASM=tailcall (b36ddb5b, round 3): the
  RET-to-symbol path in `src/cmd/internal/obj/wasm/wasmobj.go` emits
  return_call instead of i32.const 0; call $target; return. It must stay
  off by default: node 22 (V8) validates and executes return_call, but
  wazero 1.12 rejects such modules at compile time ("feature tail-call is
  disabled") and its CLI has no flag to enable it. Revisit the default
  when wazero catches up.
- P2, fork-fixable (residual; DWARF emission itself landed round 4, see
  the table): wasm DWARF variable locations are placeholders. Variable
  DIEs carry names, types, and declaration positions, but their location
  expressions are CFA-relative stack offsets with no .debug_frame to
  define a CFA (wasm has no machine registers, so there is no register
  mapping and location lists stay off); consumers can walk the DIE tree
  and step by line but cannot print variable values. Faithful locations
  need DW_OP_WASM_location expressions describing wasm locals and Go's
  linear-memory pseudo-registers. Also: the emitted address_size is 8
  (wasm PtrSize; clang emits 4 for wasm32) - every llvm tool accepts 8,
  but the Chrome DevTools C/C++ debugging extension is unverified end to
  end against it. go test binaries omit DWARF by design (cmd/go's
  OmitDebug path - they are throwaway host-run artifacts).
- P2, fork-fixable: Codegen perf leftovers (round 2 fixed the two big ones,
  int64 division and the atomics - see the table): non-provably-bounded
  shifts pay a bounds Select; everything is widened to i64 with wrap/extend
  traffic on pointer ops; spills go to linear memory (16 pseudo-registers).
  bits.Add64/Sub64 intrinsics were prototyped 2026-07-05 and benchmarked:
  no measurable win over the pure-Go lowering, so they were not kept.
- P3, inherent-ish: Functions are capped at 65536 blocks (16-bit PC_B);
  the funcref table carries 4096 dead slots; buildmodes are exe-only on js
  (c-shared exists on wasip1 only); no cgo, race, msan, asan, or fuzzing on
  either port.

### wasip1 and stdlib gaps

- P0 class, document (portable fix needs wasi-sockets): The fake network
  remains the DEFAULT on both ports. net.Listen/Dial succeed against an
  in-memory, process-local network (`src/net/net_fake.go`); listeners are
  unreachable from outside, dials to real hosts fail ECONNREFUSED, DNS
  resolves over the same fake net and fails misleadingly, and UDP writes to
  nonexistent peers still return success while dropping every byte
  (`src/net/net_fake.go:1113`, unfixed). Escape hatches:
  GOWASI=wasmedgesock for real TCP on wasip1 (this fork, round 4 - see the
  table; requires a host implementing the WasmEdge socket extension, such
  as WasmEdge itself or the testdata/wasip1sock reference host; TCP with
  IP-literal addresses only), GODEBUG=jsfetchnode=1 for HTTP under node
  (this fork), browser fetch for HTTP on js (upstream), and on wasip1
  inherited listeners - net.FileListener over a host-preopened socket fd
  with sock_accept; zero-value remote addresses. UDP, DNS, and unix
  sockets stay fake even under wasmedgesock; the portable fix is
  wasip2/wasi-sockets (golang/go#65333, #67673, #77141).
- P1, part fork-fixable: wasip1 file metadata is fiction: Chmod/Fchmod
  silently succeed doing nothing (`src/syscall/fs_wasip1.go:711`), stat
  synthesizes 0700/0600 modes and uid/gid 0. Honest ENOSYS for Chmod is a
  one-liner but breaks code that "worked"; left as documented behavior for
  now.
- P1, inherent: No subprocesses on either port (StartProcess/Wait4 ENOSYS,
  no fork/exec in wasm or WASI p1); os.Pipe and Dup are ENOSYS too.
- P1, inherent: Signals are never delivered on either port (_NSIG=0). Notify
  compiles and registers channels that can never fire (and, after this
  fork's fix, no longer burns CPU on wasip1). time.Sleep is uninterruptible.
- P1, inherent: In a browser the default filesystem is ENOSYS-everything
  except stdout/stderr writes; real fs exists only under node. js time zones
  are a fixed-offset snapshot of the current UTC offset, so wall times in the
  other DST phase are wrong (`src/time/zoneinfo_js.go:20`).
- P2, inherent: One blocking host call halts the world (all goroutines and
  timers) - single thread, no event loop on wasip1. Mitigated for stdio and
  pollable fds via nonblocking mode plus the poll_oneoff netpoller, but only
  when the host supports it (tetratelabs/wazero#1538, golang/go#62304).
- P2, document: identity/introspection are canned: os.Executable errors,
  Hostname is "wasip1"/"js", uid/gid are constants; os.Getwd on wasip1 is
  bookkeeping from $PWD/first preopen, never validated against the host.
- P3, document: browser clocks are Spectre-coarsened (sub-ms timing
  unreliable); js PathMax is 256; the wasip1 poller supports at most 65535
  subscriptions and netpollBreak is a no-op; reactor (c-shared) instances
  execute nothing between host calls - timers fire late or never, and a
  blocked export is a fatal deadlock.

## Performance cost of the preemption fix

The preemption fix (aa31fde9) inserts a guard check on every backedge of
every reducible loop in every non-nosplit function. The honest numbers,
measured on this tree:

- Worst case, unarmed: a 3-instruction loop body (x = x*c1 + c2) originally
  slowed down 31.7% under node (js/wasm) and 51.2% under wazero
  (wasip1/wasm). Round 2 (5f9934ce) fused the guard into a single 32-bit
  machine op; on the round-2 reference container (1e9 iterations, best of
  3, ns/iteration) the same loop measures:

  |            | no guard | round-1 guard | round-2 guard |
  |------------|----------|---------------|---------------|
  | node 22    | 2.90     | 4.25          | 4.24          |
  | wazero 1.12| 1.83     | 3.92          | 3.20          |

  wazero executes every instruction, so the shorter check pays directly
  (-18% wall time on the worst case); V8 hoists the disarmed-case guard
  load out of the loop either way, so node is bounded by the compare+branch
  itself. This remains the theoretical worst case - the check is a fixed
  cost per iteration, so real loop bodies pay proportionally less, and
  package benchmark suites showed no pathological slowdown.
- Binary size: +4.4% on a representative binary (two extra blocks plus the
  guard per loop backedge); round 2 claws back ~4.4KB of that on hello.wasm
  (one byte per guard plus the dropped extend/round-trip).
- Latency while armed: an armed loop yields at most every 100us (the clock is
  read every 64 gate calls), so a timer can fire up to ~100us late plus the
  time for 64 iterations. Checks are armed only when there is pending work
  (runnable goroutines, due timers, active GC, netpoll waiters, or a
  stop-the-world request) and disarmed otherwise.
- Opt-out: build with GOEXPERIMENT=nopreemptibleloops to get upstream's
  original codegen (and upstream's original hangs) back.
- The round-1 follow-up idea (compare only 32 bits of stackguard1) is done,
  see above. Correctness argument: on wasm stackguard1 is only ever 0
  (disarmed; sp32 < 0 is always false unsigned) or stackPreempt (armed; low
  word 0xfffffade exceeds any real stack pointer, the same "stackPreempt is
  greater than any real sp" assumption the runtime already makes), so the
  low-word compare gives the same answer as the 64-bit one.

## Performance cost of CPU profiling

CPU profiling (ebf7b6a5, round 3) samples from the same loop-preemption
gate, so collecting a profile keeps the preemption checks armed for every
running goroutine for the length of the profiling window:

- Worst case while profiling: a 2-instruction loop body runs ~6.5x slower
  under node and ~5x under wazero (loops containing a call pay about 2x).
  It would be ~15x/~12x, but goschedguarded now batches inline: 63 of
  every 64 armed backedge hits cost one call and a few loads instead of
  two calls and the full gate. That same batching also cheapens the
  round-1 armed windows (pending work, active GC), profiling or not.
  When nothing is being profiled the checks disarm as before and the
  round-2 numbers above apply.
- When only profiling (and no scheduler work) keeps the checks armed, the
  gate declines the 100us cooperative yields, so a profiled program is
  not forced through pointless scheduler passes.
- Sampling bias, documented in the code: samples land only at loop
  backedges of non-nosplit functions. Straight-line stretches, loopless
  recursion, nosplit runtime code, system-stack code, and host calls are
  attributed to the next backedge the goroutine reaches, and the sampling
  deadline is wall time, not CPU time. Hot loops - where CPU-bound wasm
  programs spend their time - are exactly the instrumented points.
  Programs built with GOEXPERIMENT=nopreemptibleloops have no
  instrumented backedges and keep producing empty profiles.

## Using wasm on this fork

- This fork's `bin/go` DEFAULTS TO GOOS=cosmo. Always pin the target:
  `GOOS=js GOARCH=wasm go build .` or `GOOS=wasip1 GOARCH=wasm go build .`
  (and `GOOS=linux GOARCH=amd64` when rebuilding host tools).
- Exec wrappers live in `lib/wasm/` (not misc/wasm): go_js_wasm_exec,
  go_wasip1_wasm_exec, wasm_exec.js, wasm_exec_node.js. Put `lib/wasm` on
  PATH and `GOOS=js GOARCH=wasm go test <pkg>` / `go run .` just work.
- js/wasm needs Node.js 18 or newer (checked at startup). wasip1 needs a
  WASI preview 1 runtime; wasmtime is the wrapper default, wazero works via
  `GOWASIRUNTIME=wazero`.
- argv+env budget on js is now 61440 bytes (was 8KB) - normal CI
  environments fit without trimming.
- HTTP under node: `GODEBUG=jsfetchnode=1` enables the real Fetch transport;
  default remains the fake network (tests depend on it).
- Time zones on wasip1: set TZ and either import `_ "time/tzdata"` or run
  with a preopened zoneinfo directory (or $ZONEINFO).
- The fake network is still the default on both ports: no external
  connectivity without the escape hatches above.
- Tight loops are preemptible by default; opt out with
  GOEXPERIMENT=nopreemptibleloops if you need to compare against upstream
  behavior.
- CPU profiling works on both ports (round 3): pprof.StartCPUProfile and
  `go test -cpuprofile` produce real 100Hz profiles; heap/goroutine/
  block/mutex profiles already worked. See the sampling-bias notes above.
- `go tool objdump`, `go tool nm`, and `go tool addr2line` understand
  linked wasm binaries (round 3), including -ldflags=-s stripped ones.
- Frame-driven js apps (round 5): call the `go_gc_mark_step(budgetMs)`
  wasm export with the leftover frame budget between frames; the runtime
  performs up to that much GC mark work off the frame's critical path
  (no-op when no cycle is active) and returns whether work remains. The
  pacer detects the donations and keeps background marking out of frames.
  See `testdata/framebench` for a complete Node.js harness and measured
  numbers.
- stdout/stderr writes are synchronous under node (round 3): printing
  returns immediately and keeps program order even while another
  goroutine is CPU-busy, and an os.Exit after a print always runs.
- GOWASM=tailcall emits return_call for compiler-generated tail calls -
  js/node only: wazero rejects the output, so leave it unset (the
  default) for wasip1.
- Both ports are CI-gated: the `wasm` job in
  `.github/workflows/cosmo-ci.yml` builds std and runs the stdlib and
  wasmexport-testdir regression subset (including runtime/pprof since
  round 3) under node 22 (js) and wazero (wasip1) on every push.

### Threads B3 (2026-07-17): multi-P scheduler, cooperative STW, non-blocking main park

- **GOMAXPROCS unclamped under GOWASM=threads**: the env value (and
  runtime.GOMAXPROCS) is honored, capped at GOWASMTHREADSPOOL+1 (pool default
  4). Default stays 1 (NumCPU is 1); multi-P is opt-in. startm degrades
  gracefully (releases the P, drains its runq to the global queue, kicks the
  running loops) when the pool cannot provide another M.
- **Real atomics everywhere**: internal/runtime/atomic's plain wasm fallback
  bodies are replaced under wasm.threads by 0xFE assembly + wrappers
  (atomic_wasmthreads.go/.s) - sync/atomic's trampolines and the runtime's
  linknamed pointer ops (SwapPointer & co) were reaching the non-atomic
  bodies. publicationBarrier is a real atomic fence under threads.
- **Cooperative STW across threads**: preemptone/preemptall/suspendG arm the
  compiler-inserted loop backedge checks cross-thread (stackguard1), so
  allocation-free tight loops on worker Ms reach safepoints; GC (incl.
  GODEBUG=gcstoptheworld=1) works across >= 3 threads.
- **Non-blocking main park**: an idle main M releases its P and parks in the
  host event loop (pause) instead of futex-blocking; worker threads wake it
  via Atomics.waitAsync on a shared wake word (node >= 16; a pending
  waitAsync does not hold the event loop open, so the exit-time deadlock
  probe still works). While Go worker threads are active the host keeps the
  loop alive (runtime.wasmSetKeepAlive). Idle-P and busy-P timers are
  backstopped by the main M's JS timeout plus parked-worker timed parks.
- **syscall/js off-main**: a syscall/js call from a goroutine on a worker M
  MIGRATES the goroutine to the main thread (runtime migrate queue, popped
  only by the main M); fd 1/2 writes (fmt/println/testing output) go through
  the runtime's wasmWrite import directly on workers. Value finalizers fired
  on worker Ms are queued and released on main.
- **Resolved (B3): the "lost-wakeup" stalls / exit-time hang were two
  distinct bugs.** (1) A main-thread microtask livelock: wasm_exec.js
  keeps an Atomics.waitAsync watcher armed on the main wake word across
  every resume (arming before resume is what makes worker wakes race-free),
  so a wake-word bump issued ON the main thread from inside a resume lands
  on an armed watcher and queues the next resume as a microtask. The
  self-serve resume path did exactly that (wasmMainParkWake ->
  notewakeup(&m0.park) -> wasmWakeMainThread), so one orphan nudge seeded an
  unbounded microtask chain of self-resumes; JavaScript drains microtasks
  before macrotasks, so all JS timers and the worker-posted runtime.exit
  message starved (multi-second stalls broken only by a real cross-thread
  wake; a permanent hang when it was the exit message). Fixed by dropping
  wasmMainWake bumps issued on the main thread itself - the main M is awake
  there and re-checks every wake condition before it next parks.
  (2) Migrate-queue starvation: a goroutine that calls syscall/js on a
  worker M migrates to a queue only the main M's findRunnable can pop, and
  its only wake was the single push-time nudge - consumable without effect
  when the resumed main M could not take a P. Worse, a worker M idling in
  beforeIdle's timed sleep holds its P for the whole wait, so with
  GOMAXPROCS=1 the resumed main M NEVER got the P and the migrated
  goroutine sat unrunnable until the test timeout (the long-standing
  "default-config stall": sync.test's runExamples stuck in
  runtimeMigrateToMain). Fixed three ways: pidleput and the parked-worker
  watchdog re-nudge the main M while migrations pend; a worker's timed
  idle-hold bails out (releases the P through the ordinary give-up path)
  whenever the main M needs one (pending migrations or wasmMainWantsP);
  and wasmMigrateParkFn wakes the sched nudge word so a sleeping P-holder
  re-checks immediately.
- **Resolved (B3): rare `split stack overflow` at GOMAXPROCS>1** (runtime's
  TestReadMemStats): the wasm large-frame prologue's stack check computed
  `stackguard0 + (framesize - StackSmall)` with a 32-bit add - the literal
  "TODO(neelance): handle wraparound case". When another thread armed
  preemption (stackguard0 = stackPreempt, ~0) exactly while a big-frame
  function was entered - impossible before B3's cross-thread
  preemptone/suspendG, since nothing armed a RUNNING wasm goroutine - the
  add wrapped and the check was silently skipped, so the frame ran below
  stack.lo and the next callee's morestack died with "split stack
  overflow". cmd/internal/obj/wasm now tests the stackPreempt sentinel
  explicitly (full 64-bit compare, OR'd into the check) for big frames.
- **Known issues (B3)**: (1) parallel speedup depends on pool headroom
  (size the pool > GOMAXPROCS so a parked worker can cover far-future timers;
  otherwise CPU loops stay gate-armed and pay ~4x call overhead); (2) an
  event handler blocked forever can head-of-line-block later host events;
  (3) a rare `fatal error: wirep: invalid p state` (p->m set while _Pidle)
  in the GOMAXPROCS=4 runtime suite (~1/10 batches; not observed at the
  default GOMAXPROCS=1 or =2) - a P handoff race in the multi-P bring-up,
  tracked for the B4 bug sweep; (4) syscall/js's main-thread affinity is
  per-call best-effort: a goroutine can in principle be preempted and
  migrated off the main M between mustBeMainThread's migrate and the host
  call (the Value-finalizer instance of this TOCTOU is fixed by always
  queueing; full affinity/host-call forwarding is B4).
  Remaining for B4: full main-thread affinity/host-call forwarding,
  memory.grow coordination audit, dedicated mark worker knobs, browser hosts.
