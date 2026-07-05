# WebAssembly Port Shortcomings (GOOS=js, GOOS=wasip1)

This document catalogs the state of the two WebAssembly ports in this tree:
what this fork has fixed, what remains broken or missing, what each remaining
item would take to fix, and what it costs to use the fixes. Snapshot date:
2026-07-04, based on the go1.26 tree this fork tracks. Severity: P0
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
verified in this tree under Node.js 22 (js) and wazero 1.12 (wasip1).

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
| syscall (wasip1) | Preopens matched by string prefix: with /data preopened, /database/x silently resolved inside /data | none verified | 0e665b44 | wrong file, silently -> match only on path component boundaries |
| syscall (wasip1) | Paths outside all preopens (or with no preopens) failed with EBADF "Bad file number", os.IsNotExist false | #63466, #60732 | b2706c78 | fd-bug-looking error -> ENOENT, os.IsNotExist true |
| time (wasip1) | time.Local was permanently UTC; $TZ ignored; no zone source worked | none verified | db2be627 | UTC forever -> standard $TZ handling via $ZONEINFO, preopened zoneinfo dirs, or time/tzdata |
| os/exec | LookPath reported "not found in $PATH" for files that exist | none verified | 55497db5 | misleading PATH chase -> "cannot execute binaries on wasip1/wasm", matches errors.ErrUnsupported (and still ErrNotFound) |
| net/http (js) | Node.js was hard-locked onto the fake network; every http.Get failed even though node >= 18 ships fetch | #57613, #60810 | a18df3b8 | no real HTTP under node -> GODEBUG=jsfetchnode=1 opt-in enables the Fetch transport |
| syscall/js | Reflect.get in valueIndex/valueLength can reenter Go (Proxy traps, accessors); results were stored through a stale SP after stack growth: memory corruption | none verified (present upstream) | 9b6483bb | Index/Length return garbage, stack clobbered -> SP refreshed like valueGet/valueCall already did |
| syscall/js | js.Error.Error() panicked when JS threw a non-object (throw "boom"), masking the original error | none verified | 866f91a9 | panic while printing panic -> falls back to the thrown value's string; also fixes Length's wrong panic text and stale package docs |
| syscall (js) | mapJSError panicked on any JS error without a known .code, killing the program from one failed fs op | none verified | 1c34c287 | whole-program crash -> unknown errors map to EIO, operation fails normally |
| lib/wasm | Node fallbacks were broken: require("performance") is not a builtin; the crypto fallback only worked by accident on node >= 17.4 | nodejs/node#49272 (context) | d2f24bdc | crash or late TypeError on old node -> explicit node >= 18 check with a clear error |
| lib/wasm | Browser output shim: one line buffer for both fds, everything via console.log, unterminated final output lost at exit | none verified | 3fc95897 | interleaved/lost output -> per-fd buffers, stderr to console.error, flush on exit |
| cmd/link, lib/wasm | argv+env budget was 8KB; env-heavy hosts (CI) died at startup with "exceeds limit" | #49011 | 332bb923 | startup crash under big env -> 61440-byte budget (data floor 64KB), error message reports needed/available |
| cmd/compile | go:wasmexport with 17+ small params passed the 128-byte check, then the assembler panicked (ICE "bad Get: invalid register") | none verified | 4ead4b0a | internal compiler error -> proper "too many parameters" diagnostic; 16 params still compile |
| cmd/compile | zeroRange emitted 3 instructions per 8 bytes for large stack zeroings | none verified | 41389912 | 24-instruction prologue for a 64-byte range -> single memory.fill (>32 bytes) |
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
  due, `src/runtime/netpoll_fake.go`). Upstream #26045, #34324; an Await/
  AsyncFuncOf-style API (#69720, #72045) is the usability fix, fork-fixable.
- P2, fork-fixable: The forced 2-minute GC never fires. forcegchelper is
  resumed only by sysmon (`src/runtime/proc.go:6607` area) and wasm has no
  sysmon, so an idle program never collects after its last allocation burst;
  finalizers and cleanups sit forever. Could be triggered from beforeIdle or
  a self-rearming runtime timer.
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
- P3, fork-fixable: wasip1 notetsleepg still busy-yields for TIMED waits
  (`src/runtime/lock_wasip1.go:87-106`); the infinite-wait case (os/signal)
  was fixed, but a timed note wait from a user goroutine burns CPU. Other
  landmines: notesleep/notetsleep throw on both ports; osyield is UNDEF
  (`src/runtime/sys_wasm.s:28`); js usleep is a no-op; the g0 stack is a
  fixed 8KB global with no guard (`src/runtime/sys_wasm.go:13`).
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
- P1, fork-fixable: No way to await a Promise. Every async JS API costs ~20
  lines of FuncOf/then/catch/channel boilerplate per call site, and doing it
  inside an event callback deadlocks. Upstream proposals: #69720 (js.Await),
  #72045 (AsyncFuncOf), #28911.
- P1, fork-fixable: CopyBytesToGo/CopyBytesToJS accept only Uint8Array and
  Uint8ClampedArray. WebGL/WebAudio/canvas pipelines needing Float32Array etc.
  must copy element-wise through Index/SetIndex (golang/go#32402, #25532,
  #38011; #31980 explains why the view-based TypedArrayOf was removed).
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
  in the backend), no tail calls (wrapper chains grow the wasm stack), no
  multi-value returns (single-i32 internal ABI), no externref/WasmGC
  (golang/go#63904 - blocked by interior pointers), no memory64 (upstream
  momentum is the opposite: GOARCH=wasm32, #63131). GOWASM feature gating is
  an empty struct today (`src/internal/buildcfg/cfg.go:333`), so there is no
  mechanism to introduce gated features without adding it back.
- P2, fork-fixable (bounded): No DWARF is ever emitted for wasm
  (`src/cmd/link/internal/ld/dwarf.go:1720`), `go tool objdump` cannot
  disassemble wasm, and no debugger supports the port - debugging is
  name-section stack traces only. The name section is on by default and
  costs hundreds of KB; `-ldflags=-s` strips it (worth documenting; a
  wasm-opt -Oz pass typically shaves another 10-20%).
- P2, fork-fixable: Codegen perf leftovers: int64 division is a runtime call;
  non-provably-bounded shifts pay a bounds Select; bits.Add64/Sub64 are not
  intrinsified; every sync/atomic op is a full cross-package call despite the
  single-threaded target; everything is widened to i64 with wrap/extend
  traffic on pointer ops; spills go to linear memory (16 pseudo-registers).
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

- Worst case, unarmed: a 3-instruction loop body (x = x*c1 + c2) slows down
  31.7% under node (js/wasm) and 51.2% under wazero (wasip1/wasm). This is
  the theoretical worst case - the check is a fixed cost per iteration, so
  real loop bodies pay proportionally less, and package benchmark suites
  showed no pathological slowdown.
- Binary size: +4.4% on a representative binary (two extra blocks plus the
  guard per loop backedge).
- Latency while armed: an armed loop yields at most every 100us (the clock is
  read every 64 gate calls), so a timer can fire up to ~100us late plus the
  time for 64 iterations. Checks are armed only when there is pending work
  (runnable goroutines, due timers, active GC, netpoll waiters, or a
  stop-the-world request) and disarmed otherwise.
- Opt-out: build with GOEXPERIMENT=nopreemptibleloops to get upstream's
  original codegen (and upstream's original hangs) back.
- Follow-up idea: the guard currently loads and compares a 64-bit word
  (stackguard1); a 32-bit compare would shave a fraction of the per-iteration
  cost on engines that do not fold the widening.

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
