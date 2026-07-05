# WebAssembly Port Shortcomings (GOOS=js, GOOS=wasip1)

This document catalogs the state of the two WebAssembly ports in this tree:
what this fork has fixed, what remains broken or missing, what each remaining
item would take to fix, and what it costs to use the fixes. Snapshot date:
2026-07-05 (round 2), based on the go1.26 tree this fork tracks. Severity: P0
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
round (2026-07-05) of codegen, runtime, and interop work built on that base;
its entries are dated below where the distinction matters.

## Fixed in this fork

| Area | Problem | Upstream ref | Commit | Before -> after |
|---|---|---|---|---|
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
| docs | os/signal said nothing about wasm; net's fake network was described only as a testing aid in a source comment | n/a | 8ed4b658 | undocumented traps -> package docs state what works, what silently does not, and the escape hatches |

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
  call is in flight) - a design project, not a patch.
- P1, needs wasm proposal: One thread, one P, forever. newosproc throws
  (`src/runtime/os_wasm.go:110`), NumCPU=1, atomics are plain loads/stores
  (`src/internal/runtime/atomic/atomic_wasm.go`). Parallelism needs the wasm
  threads proposal plus a large runtime port (golang/go#28631, #56305).
- P1, inherent: Blocking inside a js.FuncOf callback still deadlocks - now
  with a clear error, but the semantics cannot change: the callback runs
  synchronously on the JS thread and nothing can block there. Worse, if any
  unrelated timer exists, the deadlock is undetectable and becomes a hot spin
  (fake netpoll returns immediately; the scheduler polls until the timer is
  due, `src/runtime/netpoll_fake.go`). Upstream #26045, #34324. js.Await
  (this fork, 5b975b77) is the usability fix for ordinary goroutines; the
  callback-cannot-block constraint itself remains.
- P2, fork-fixable (fs_js): println-then-exit teardown can hang while a
  CPU-spinner goroutine is alive (pre-existing; re-confirmed by both
  round-2 audits, then bisected against master during round-2 integration:
  identical there). Mechanism, verified 2026-07-05: fmt.Println under node
  goes through fs.write, whose COMPLETION callback runs on the JS event
  loop; a busy spinner never lets wasm return there, so the printing
  goroutine blocks forever and never reaches its os.Exit - the bytes do
  reach the pipe, the process just never exits (a special case of the P1
  host-starvation item above). The builtin println (synchronous wasmWrite
  import) is immune, and a reached os.Exit is immediate under node
  (wasm_exec_node.js wires go.exit straight to process.exit). Follow-up
  idea: route the stdout/stderr fast path in fs_js.go through fs.writeSync
  under node, so terminal output cannot park a goroutine on the event
  loop.
- P2, inherent: Linear memory never shrinks. wasm has no memory.shrink;
  the sbrk allocator can reuse but not return (`src/runtime/mem_sbrk.go`).
  Peak footprint persists until the instance dies (golang/go#59061, #27462).
  The background scavenger runs and accomplishes nothing.
- P2, fork-fixable: faketime is broken on wasm: on js the beforeIdle path
  makes checkdead's timejump unreachable (fake clock never advances, program
  hangs re-arming real timeouts); on wasip1 the timejump path throws
  "notesleep not supported by wasi" (`src/runtime/lock_wasip1.go:77`).
- P2, needs design: No CPU profiling. setProcessCPUProfiler/
  setThreadCPUProfiler are empty stubs (`src/runtime/os_wasm.go:149`); there
  is no SIGPROF and no host timer sampler. pprof CPU profiles are empty
  (heap/goroutine/block/mutex profiles work).
- P3, audited non-issue (2026-07-05) with one live case: wasip1 notetsleepg
  still busy-yields for TIMED waits (`src/runtime/lock_wasip1.go:87-106`),
  but no timed caller is reachable on wasip1 - sysmon's is gated by
  haveSysmon, and the stop-the-world notes cannot time out with
  gomaxprocs==1. The one reachable untimed busy-yield is the profbuf reader
  during an active pprof CPU profile (measured: 5.1s of user CPU for a 5s
  profiling window that collects zero samples) - subsumed by the
  no-CPU-profiling item above; fix both together by parking the reader.
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
  in the backend), no tail calls (wrapper chains grow the wasm stack; see
  the next bullet - the proposal is standardized but the default wasip1
  runtime cannot run it), no multi-value returns (single-i32 internal ABI),
  no externref/WasmGC (golang/go#63904 - blocked by interior pointers), no
  memory64 (upstream momentum is the opposite: GOARCH=wasm32, #63131).
  GOWASM feature gating is an empty struct today
  (`src/internal/buildcfg/cfg.go:333`), so there is no mechanism to
  introduce gated features without adding it back.
- P2, blocked on engine support: wasm tail calls (return_call) were
  investigated 2026-07-05 for the RET-to-symbol path in
  `src/cmd/internal/obj/wasm/wasmobj.go` (currently i32.const 0; call
  $target; return): node 22 (V8) validates and executes return_call, but
  wazero 1.12 rejects such modules at compile time ("feature tail-call is
  disabled") and its CLI has no flag to enable it. Emitting return_call
  would break the default wasip1 runtime pairing, so it stays off (or needs
  a GOWASM gate, see above) until wazero catches up.
- P2, fork-fixable (bounded): No DWARF is ever emitted for wasm
  (`src/cmd/link/internal/ld/dwarf.go:1720`), `go tool objdump` cannot
  disassemble wasm, and no debugger supports the port - debugging is
  name-section stack traces only. The name section is on by default and
  costs hundreds of KB; `-ldflags=-s` strips it (worth documenting; a
  wasm-opt -Oz pass typically shaves another 10-20%).
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

- P0 class, document (real fix needs wasi-sockets): The fake network remains
  the DEFAULT on both ports. net.Listen/Dial succeed against an in-memory,
  process-local network (`src/net/net_fake.go`); listeners are unreachable
  from outside, dials to real hosts fail ECONNREFUSED, DNS resolves over the
  same fake net and fails misleadingly, and UDP writes to nonexistent peers
  still return success while dropping every byte (`src/net/net_fake.go:1113`,
  unfixed). Escape hatches: GODEBUG=jsfetchnode=1 for HTTP under node (this
  fork), browser fetch for HTTP on js (upstream), and on wasip1 inherited
  listeners only - net.FileListener over a host-preopened socket fd with
  sock_accept; no outbound dial, zero-value remote addresses. Real sockets
  need wasip2/wasi-sockets (golang/go#65333, #67673, #77141).
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
- Both ports are CI-gated: the `wasm` job in
  `.github/workflows/cosmo-ci.yml` builds std and runs the stdlib and
  wasmexport-testdir regression subset under node 22 (js) and wazero
  (wasip1) on every push.
