# Session Reconstruction: "Session lock feature implementation"

_Source: `~/Downloads/session-lock-feature-implementation-20260907-123222.db.zst` (29 MB → 194 MB SQLite, 130,003 events, ~18,700 messages). Session ran 2026-09-06 08:29 UTC → 2026-09-07 17:15 UTC (~33 hours), Claude Code on `claude.ai/code`._

This document reconstructs everything that happened in the session — what was done, where the work stands, and what still needs doing. It is a **summary**; specifics like commit SHAs, syscall names, and file paths are captured so you can pick any thread back up.

---

## 1. The original goal and what it really became

The session title is **"Session lock feature implementation"**, but that name is a narrow slice of what actually happened.

**The stated goal (set via `/goal`, verbatim):**

> "Implment this unimplemented gosmopolitan feature, and any other unimplmeented features"
> — the error being solved: `slh: app: lock session /Users/mhaynie/.local/share/slh/sessions/01M1TX6M0951Y027E5A5NHRVAB: function not implemented`

**What that points at:** the tool `slh` (`simple-llm-harness`) locks its session file via `syscall.Flock(fd, LOCK_EX|LOCK_NB)` in `internal/app/sessionlock_unix.go` (read-only) and reads `EWOULDBLOCK` as "session already open elsewhere." On macOS, `syscall.Flock` returned **ENOSYS** because `SYS_FLOCK` was absent from the cosmo Darwin emulation — so the "session lock" of the title is literally the lock slh takes on its own session.

**Where it went:** implementing flock(2) turned into a sprawling, ~33-hour port-hardening effort. The **goal was extended mid-session** with binding clauses:
- "All binaries emitted must be APE or WASM"
- "Make sure that runtime.GOOS and runtime.GOARCH matches the host OS dynamically"
- "You aren't done until all of these tasks are completed and CI is green. If anything is shortcutted or incomplete or cheated … you have failed."

**Primary repo/branch:** `wow-look-at-my/gosmopolitan` on branch `claude/session-lock-feature-hxi7mp`, PR **#128** (later work on PR **#130**). Also touched: `go-s3-server`, `gh-wait-ci`, `simple-llm-harness`.

> Note: `gosmopolitan` is a fork of the **cosmopolitan libc** — a Go stdlib port that runs one "fat binary" (an APE, "Actually Portable Executable") across Linux / macOS / Windows hosts. The session's core technical work is implementing and debugging the syscall/ABI layer of that port across its three host personalities.

---

## 2. Flock implementation (the actual original bug) — DONE

**Diagnosis:** `syscall.Flock` reached no emulation on any non-Linux host and answered ENOSYS. The errno slh needs is `EWOULDBLOCK` (the "already open elsewhere" signal), not `EACCES`.

**The three host implementations:**

- **macOS arm64** — resolved `flock` by name via `dlsym` and dispatched `SYS_FLOCK` to it. Because it is a variadic libc call on arm64-apple, args must be passed **on the stack** through `darwinCallVariadic1` (never `darwinCall`). Added to `DarwinFns` (`DarwinFns.Flock`), consts `sysFLOCK=32` etc., six new dispatch cases in `src/internal/runtime/syscall/cosmo/syscall_cosmo_arm64.go`.
- **macOS amd64** — issued the raw XNU syscall directly (BSD 131, the number `zsysnum_darwin_amd64.go` records): `SYS_flock 73`, `SYS_fdatasync 75`, `SYS_sync 162`; XNU numbers `XNU_sync 0x2000024`, `XNU_fdatasync 0x20000bb`, `XNU_flock 0x2000083`, three new assembly dispatch labels in `asm_cosmo_amd64.s`.
- **Windows NT** — Windows has no `flock`; the NT layer maps it onto **`LockFileEx` / `UnlockFileEx`** over the whole byte range (`ntEmuFlock` in `src/runtime/os_cosmo_nt_sys.go`, symbols via optional dlsym in `os_cosmo_nt.go`). `LOCK_NB` answers `EWOULDBLOCK` rather than the shared table's `EACCES`.

`LOCK_SH/EX/NB/UN` pass through unchanged on macOS (Linux took those values from BSD). Covered by a runtime **probe** (`testdata/runtimeprobe/flock.go`) that executes on the macOS/Windows CI runners, with every step asserting what the *other* descriptor can do afterwards (a stub that answers 0 locks nothing and fails the error-only check).

---

## 3. The broader "unimplemented syscalls" survey (STUBS-INVENTORY) — MOSTLY DONE

The inventory claimed the **macOS arm64 surface was "complete" — flock disproved that.** The assistant audited it *mechanically* (diff every `SYS_*` the cosmo syscall package names against the dispatch tables) rather than by eye. Found and fixed several more:

- **Six missing macOS arm64 calls:** `flock`, `ioctl`, `getrusage`, `gettimeofday`, `fdatasync`, `sync`. Two needed real ABI conversion, not a forward: **`getrusage`/`gettimeofday`** — Apple's `timeval` has a 32-bit µs field with padding where Linux carries 64 bits; forwarded buffers keep their size but lose their contents. Conversions live in `darwinabi_cosmo.go`.
- **`mincore` on darwin** returned -1 unconditionally — its one caller reads that as "try the next size," so every page-size probe silently fell back to 256K. Now calls Apple's `mincore` via dlsym.
- **NT signal mask stubs** (`rt_sigaction`, `rtsigprocmask`): one was unreachable (routed elsewhere already, now a crash-poke); the other was reachable and is now **real** — the runtime keeps its own signal mask, a blocked signal waits pending, and unblocking delivers what waited.
- **NT `sync`** emulated via `FlushFileBuffers` over every open fd (the reachable part of "flush everything"). **NT `uname`** via `RtlGetVersion` + `GetComputerNameW` (Sysname/Machine are constants on NT: the payload *is* the host).
- **`os.Hostname` on macOS**: `uname` returns an empty `Nodename` on darwin, so `os.Hostname` fell through to non-existent `/proc/sys/kernel/hostname`. Fixed via `sysctlbyname("kern.hostname")` on arm64 and raw `__sysctl` (`{_CTL_KERN=1, _KERN_HOSTNAME=10}`) on amd64. Same push fixed `nanotime` on macOS (Ap accelerating `clock_gettime_nsec_np`; Apple's `clock_gettime` is µs-resolution, which broke the fips140 timing-jitter entropy source) and the Mkdir sticky bit.
- **fcntl record locks** (`F_GETLK`/`F_SETLK`/`F_SETLKW`): `syscall.Flock_t` describes the Linux layout; added `cosmo.DarwinFlock` (24 bytes vs Linux 32) and `darwinFcntlFlock` to convert, in `src/syscall/flock_cosmo.go`. `LOCK_NB`→`EWOULDBLOCK`; same commit fixed `sysFCHMODAT` `AT_SYMLINK_NOFOLLOW` translation.
- **`*at` family row corrected:** the inventory claimed macOS-Intel `*at` syscall numbers were "unguessable" — wrong; the vendored `zsysnum_darwin_amd64.go` carries them (`OPENAT 463`, `RENAMEAT 465`, `FSTATAT64 470`, `LINKAT 471`, `UNLINKAT 472`).
- **Real, honestly-reported limitations** (rows that *cannot* be closed, not bugs): macOS-Intel has **no CI runner** (never executed anything); Windows/arm64 has **no APE boot path**; the termios family and statfs were deferred gaps recorded in the inventory rather than guessed.

**Governing rule the user imposed (and the assistant kept):**
> "you only get to delete rows when you implement and confirm them" — and "STUBS-INVENTORY should be 100" / "100% empty by the time you're done."

The assistant did **not** satisfy "empty" by deleting the file; rows leave only when implemented **and** confirmed by a green run. It rewrote the file to ~40 lines / 5 sections, splitting fixed-but-unconfirmed rows from closed ones.

---

## 4. termios ioctls (TCGETS/TCSETS) on macOS — DONE (+ one follow-on bug)

A terminal library's "put a terminal in raw mode" path. Nothing lines up between Linux and Apple:
- Apple's flag words are **64-bit** vs Linux **32**; 20 control characters vs 19, numbered differently; line speeds in their own fields vs Linux's `c_cflag`; two collisions that **silently misconfigure**: Linux `IXON` is Apple `IXOFF`, and Linux `IUCLC` is Apple `IXON`.
- Implemented as a **struct converter** (`src/internal/runtime/syscall/cosmo/termios_cosmo.go`): `DarwinTermios` (72 B) ↔ `LinuxTermios` (36 B), bit tables, `termiosCcIndex [19]int8`, `termiosBauds`, read-modify-write (`mergeLinuxBits`) so Apple-only bits survive a Linux caller's set. Linux-only flags (IUCLC, XCASE, OLCUC, delay fields) are dropped — no in-use driver implements them. Unencodable line speeds refused in both directions.
- Tables unit-tested where cosmo tests run; a real-**pty** probe (`testdata/runtimeprobe/pty.go`) sets raw mode on `/dev/ptmx` and reads it back. Self-caught bugs: wrong `CSIZE` shift (Apple's `0x300` is 4 bits left of Linux's `0x30`, so `csizeShift = 4`), two missing commas.

### #44 (follow-on): darwin termios nosplit/stack-split — FIXED at `48727726`
Discovered via the macOS CI log, no macOS hardware needed. `cmd/go`'s package init calls `term.IsTerminal`→TCGETS, and `syscall.Syscall` has already run `entersyscall` by then. Inside a syscall window a stack split is fatal, and `darwinTermiosIoctl`, the two converters, the two baud lookups, and the two bit helpers weren't the `nosplit` functions they had to be. The real cost was `range` over full arrays (≈312+312+344+344 bytes of frame copies); switching to slicing a range took the deepest chain from **888 → 240 bytes**. Made all seven functions in the chain `nosplit`. Verified by frame-size measurement (`-gcflags=-S`); CI leg is the real proof. Cannot run on the linux-host `dist test`.

### #51 (new, OPEN): the linker's nosplit stack check is **not running** on this port
An 8192-byte over-budget `//go:nosplit` frame on `darwinTermiosIoctl` **links clean with exit 0** where upstream errors at the 792-byte limit ("nosplit stack over 792 byte limit" with the offending chain). Consequence: an over-budget nosplit chain here **does not fail the build** — it writes past the stack guard at runtime, which is silent memory corruption. Every nosplit path in the port (the darwin syscall spine is almost entirely nosplit) is unguarded. Next step: find why `cmd/link/internal/ld`'s stack check is skipped for `GOOS=cosmo` — whether `doStackCheck` is reached, whether the APE/fat-link route bypasses it, or whether the fork disabled it during bring-up.

---

## 5. The macOS leg: APE-exec (root cause of the mass failure) — FIXED

The single biggest thread. The macOS leg failed **every** package with ~1,394 lines of `fork/exec …: exec format error`.

**Root cause:** a unix kernel refuses the APE's `MZqFpD='` header — `execve` returns **ENOEXEC** without a root-only `binfmt_misc` entry. So **no cosmo binary could start another cosmo binary** on macOS. That took down `t.Fork`, `t.Setenv`, `t.Chdir`, and the whole `AllocsPerRun` family (which forks rather than asking tests to opt in).

**Why it appeared now:** a prior commit exposed the cosmo port to CI testing. Before that, `dist test` built host ELF binaries and tested the Linux port; the cosmo paths were never exercised.

**Fix:** `src/syscall/exec_cosmo_ape.go` — detect the APE (`apeMagic`), and on ENOEXEC retry `forkAndExecInChild`/`Exec` through **`/bin/sh`** (the same fallback `misc/cosmo`'s wrappers apply from outside). Proven red→green locally with `binfmt_misc` disabled, reproducing the exact CI error. A later refinement fixed the **APE self-locate** for a bare `argv[0]` (a CGI child's PATH search; resolved like `execvp` so the loader gets an absolute path).

**Important second half (`exec_libc2.go`):** on macOS, `bin/go` itself is a **native darwin (Mach-O) binary** and never reaches the cosmo syscall package — so `os/exec` passed but `cmd/link`/`cmd/nm`/`cmd/objdump`/`cmd/pprof`/`cmd/cover`/`cmd/vet`/`cmd/go` toolexec/`net/http/cgi` (~50 tests, ≈12 of 18 macOS failures) still failed. The actual fix for those was in **`src/syscall/exec_libc2.go`** — the darwin child retries through `/bin/sh`. The assistant explicitly retracted the first pass as a misdiagnosis that only fixed cosmo-built binaries.

---

## 6. The Windows leg: "every package exits 2 with no output" (task #17) — DIAGNOSED

A **second, separate cause** from the APE-exec wall. Every package fails in ~0.1s with a bare `exit status 2` and **no output**, so the test binary dies before printing anything.

**Progression:**
- No cosmo binary runs on that host at all — fizzbuzz (fat or thin) exits 2 and writes nothing in three argument shapes; `master`'s windows leg is also red, so the branch didn't introduce it. Exit 2 + no output = a runtime `throw` whose print goes nowhere, i.e. a panic **before the NT layer caches its std handles**.
- A dedicated **winbisect CI job** bisected the NT boot against the branch's own ~80 commits: every published master toolchain through v476 builds a working fizzbuzz; every branch commit from **`4e8ff115`** ("runtime: report the host in GOOS and GOARCH") on exits 2. That commit made `runtime.GOOS/GOARCH` **variables** (needed to "report the host dynamically") and touched the NT boot path — the regression. Master `051a06ba`, `2b5c9490`, `e2f008f8` all run.
- Follow-on instrumentation (`ntwrite1`, `e8874ce7`) resolves `WriteFile` and the std handles from the **loader-filled IAT** so a `throw` before `ntResolve` prints instead of dying silently; added `-tags cosmontdebug` milestones through `runtime.main`. Some bisect legs showed **exit 139** (segfault) vs the build leg's 2.
- **winbisect job** was (as planned) removed once it had named the answer.

**Status at session end: OPEN but diagnosed-not-fixed** — the branch's dynamic `GOOS/GOARCH` change is the regression, running against the *necessity* of matching `runtime.GOOS/GOARCH` to the host.

---

## 7. debug/elf-vs-APE test family (task #13) — FIXED

`cmd/addr2line`, `cmd/link`, `cmd/compile/internal/ssa`, and `runtime`'s `TestUnsafePoint` open a linked binary with `debug/elf` or `go tool nm` and hit the APE header (`bad magic '[77 90 113 70]'` = "MZqF").

**Why it was hard to reproduce:** an APE **assimilates itself on first run on Linux** — the loader writes a section-less ELF header over the first 64 bytes of a staged copy (as root, bind-mounted over the original). So **only a never-executed binary still shows the APE magic**. macOS does not assimilate, which is why exec failures reproduced there but not on Linux.

**Fix:** new `src/internal/ape/ape.go` — single source for container facts (`Magic="MZqFpD='"`, `HeaderSize=65536`), plus `Payload(r io.ReaderAt)` and `Sidecar(name)`. `debug/elf`'s `NewFile` re-targets to `ape.Payload`, `Open` prefers the sidecar. One change fixed `cmd/addr2line` and most `cmd/link` tests.

---

## 8. The fork's parallel test framework & `t.Serial()` — LARGELY DONE, key deadlock FIXED

**The deep cause of most remaining failures:** this fork's `testing` package runs **every top-level test in parallel by default** (unlike upstream Go), and it adds `t.Serial()` (process-wide exclusive barrier), `t.Fork()`, and implicit forking on `t.Setenv`/`t.Chdir`. When CI began running the whole Go distribution test corpus, hundreds of stdlib tests written for sequential execution began racing on package globals.

**The sweep:** added `t.Serial()` across stdlib tests that mutate package globals — `text/template/parse`, `expvar`, `flag`, `fmt`, `mime`, `net` (many files), `crypto/x509`, `cmd/gofmt`, `cmd/pack`, `cmd/asm`, `cmd/objdump`, `cmd/compile/internal/ssagen`, `cmd/go/internal/{work,cache}`, plus later `iter`, `log/slog`, `net/http`, `crypto/tls`, `encoding/binary`, `runtime`, `go/internal/srcimporter`, `go/types`, `testing/iotest`, `syscall/js`, `cmd/compile/internal/inline`. Highlight fixes: `runtime` GOMAXPROCS (12 tests, `d30ba46d`); `cmd/compile/internal/inline` loop tunables (`86f27fc1`); `http2`'s `SetForTest` fixed in the *helper* (`3f0350ed`); `testing` fork-env fix that un-breached `cmd/pack` (`startEnv = os.Environ()`); and making the `testing` internal `barrierHeld` **atomic** (`f3fa6a81`, an `int32`→`atomic.Int32` conversion — a stale read of an ancestor's hold decided parallelism wrongly for every package).

**The vet analyzer (a narrative beat):** an early analyzer attempt was deleted by the user's scorn; the **surviving** one, `cmd/vet/internal/testglobals`, walks each `TestXxx(*testing.T)`, finds writes resolving to a package-scope var, and reports unless the test calls `t.Serial()`/`t.Fork()`. It's wired into `defaultVetFlags` (runs on every test build) and found **108 fixable spots across 54 files**, applied by a small Go fixer driven off the analyzer. Commit `f765127a`.

### #47: the Serial-barrier deadlock — FIXED at `ad03e9af`
The `time` package hung **9m52s** (600s timeout) on ubuntu — `TestAfterQueuing`, `TestAfterStop`, `TestLoadLocationFromTZData`, `TestUnmarshalTextAllocations`. A real circular wait reachable when a `Serial` lands on the wrong interleaving:
1. One test's `t.Serial()` takes the barrier's **write** lock, waiting for readers to drain.
2. Go's `RWMutex` then blocks every new `RLock` behind that writer.
3. `t.Run` (from other tests) yields and **re-acquires** the barrier, queuing behind the writer.
4. A test still **holding** the read lock is itself blocked on a `sync.Once` owned by one of the barrier-blocked tests.

It can hang **any** package. The fix split the two conflated cases with a new **`serialGate`**: `acquire` (a test *starting*) still waits out a queued `Serial` caller (anti-starvation); `resume` (returning from `t.Run`) waits only for a `Serial` caller that is actually *running*, so the cycle can't close. The original hang never reproduced locally (interleaving-dependent), so the claim is the cycle is no longer *expressible*; the agent pinned it with a **negative control** (restoring the waiting-writer check to `resume` reproduces the exact failure).

### The synctest bubble deadlock — FIXED at `af276841`
`http2`'s `SetForTest` calls `t.Serial()` from *inside* a synctest bubble; the barrier is a process-wide wait and nothing inside the bubble can signal it, so synctest's deadlock detector fires (`panic: deadlock: all goroutines in bubble are blocked`). Fix: **hoist `Serial()` to before the bubble opens** (it's idempotent). Exactly two tests in the tree had this shape; both fixed.

---

## 9. Other cross-cutting fixes (brief)

- **`runtime.GOOS`/`GOARCH` as host-reporting variables** — one APE boots on three OSes, so the port built for doesn't name a kernel. GOOS/GOARCH became variables set in `osinit`; `os.Root` trailing-slash rules stopped following the POSIX branch on Linux. This also surfaced a real hole (`sigreturn__sigaction` declared for both arches but only defined for amd64 — masked by DCE). Guarded by a **`goos-readonly` hook** refusing writes to them.
- **`crypto/x509` security hole** — `Verify` used the platform verifier when `runtime.GOOS` named darwin; GOOS is now a *variable*, so a cosmo binary on macOS took the unix `systemVerify` branch that returned `(nil, nil)` — read as an accepted chain. **Every verification against the system pool succeeded, expired/self-signed certs included.** Fixed with a `hasPlatformVerifier` constant.
- **`test2json`** — `diffRaw`'s compare loop never incremented its index; any golden mismatch spun to the 600s timeout instead of printing a diff. Now fails in ~11s.
- **`cmd/compile/internal/loopvar`** — `append(cmd.Env, ...)` replaced the environment, leaving `go run` with no PATH.
- **Comment-run / no-wordspam / ste-lint** (the "don't wordspam" rule): every cosmo source under the **12-line comment cap**, docs under a 40KB/120-word/25-sentence cap, scan widened to the fork's own std packages (with a build-constraint filter for never-compiled files); `WASM_SHORTCOMINGS.md` cut **56 KB → 24 KB** (removed a 25 KB fix-history table), `DEBUGGING.md` (320 KB journal) deleted, `STUBS-INVENTORY.md` slimmed.
- **Runtime gdb tests (task #23)** — the recorded cause ("gdb rejects the APE's FreeBSD OSABI") was **disproved by experiment** (gdb warns and carries on). Real cause: cosmo builds a **stripped APE** with debug info in a **sidecar**, so gdb was handed an empty file; the sidecar loads fine. Fix is viable (sidecar runs natively) and implementation **began at session end** — still OPEN.

---

## 10. The go-s3-server cache rework (parallel sub-project) — CODE DONE, MERGE PENDING

Driven by a "cache optimization" goal. Landed in **PR #72** (merged, `a80ec0d`) and **PR #74** (branch work, pending):

- **Nagle-style batch coalescer** — first request sends immediately; coalesce only while a request is in flight (no fixed 10ms wait). Fixes the "every lookup pays 10ms on the build's critical path" shape.
- **`prefetch_only` server feature** — speculative fetch off the blocking path onto its own pool; `cmd/go` now actually stores what it fetches.
- **Put compression off the build goroutine** — `prepPool` worker pool; compression switched **lz4 → zstd**.
- **Provenance headers** (`X-Cache-Module/-Toolchain/-Target/-Client/-Kind`) + per-second server aggregate logging.
- **Windows OOM fix** — sorted-slice key index (`9705a81`, 88→31 MiB live) and **streaming batch response** (`b58467b`: one body resident at a time instead of buffering the whole `map[string][]byte`; ≈19-20% faster, 13-16% fewer allocs). `b58467b` is the streaming commit, pinned by a negative-control test.
- **`cmd/go` cacheprog output leak** (two paths) — fixed in `ddd823d`; cleared four of six ubuntu failures.
- **Defect #49 (OPEN):** a 205-request batch GET died `unexpected EOF` mid-member (12.7s). Client recovered (cost = discarded batch). Path has since been rewritten by `b58467b` to stream, so re-check against the new client.

**Final go-s3-server state:** branch head `ba3a6d9`, complete and pushed; **the merge of PR #74 is pending and must land first** (see §12).

---

## 11. Tooling repos: `gh-wait-ci` and `simple-llm-harness`

- **`gh-wait-ci`** — a Go CI-waiting script/tool, the *only* permitted Actions reader (direct Actions curl was classifier-blocked, `gh` quota exhausted). Fixed a real defect in **PR #14** (head `9b667bb`): `gh wait-ci log` refused a **finished** job's logs while siblings still ran, because the per-job fallback dropped a job on *any* error. Fix: only a **404** gets the "wait it out" message; missing credentials/network/500 return a real error. This is important because this very defect hid the final ubuntu failure (see §12). Verified, complete, pushed.
- **`simple-llm-harness`** — `slh` itself, the consumer that triggered the original flock bug (its `sessionlock_unix.go` locks the session and reads `EWOULDBLOCK`). At end, HEAD = `origin/master` at `bf6b176` (squash-merge of #38). **Deliberately left unpushed** — the branch label just sits on a commit byte-identical to master, and pushing would only create an empty PR to close. Nothing is actually unpushed.

---

## 12. WHERE THINGS STOOD AT THE END — and what still needs doing

**The session was truncated by an environment failure, not finished.** The last ~50 messages record the session temp filesystem running **completely full (ENOSPC)** — `Bash` couldn't even create its output file for `true`, `Write` got ENOSPC, and the assistant's deletion attempts never executed because no shell command ran. It was **blocked on the environment, not the work**. `M18726` is the terminal execution error. **Nothing was lost** — all repos live on `/home/user` and were pushed before the disk filled.

### Final CI state on `20346d25` (gosmopolitan, PR #130):
| job | result |
|---|---|
| wasm | ✅ success |
| build (windows-latest) | ❌ failed 16:57 (each has **one unread failure**) |
| build (ubuntu-latest) | ❌ failed 17:03 (each has **one unread failure**) |
| build (macos-latest) | 🔄 still running at truncation |

Two honest self-corrections in the tail: the assistant read a mid-flight `all-builds` snapshot ("1/4 failed: windows") as a final tally and wrongly said ubuntu went green — **for the second time in the session**. The MCP check-run path gives status but not logs; `gh wait-ci log` couldn't diagnose the unread failure because of the very #14 defect.

### Final repo state:
| repo | head | status |
|---|---|---|
| gosmopolitan | `20346d25` (PR #130) | ubuntu + windows red (unread failures), wasm green, macOS was running |
| go-s3-server | `ba3a6d9` (PR #74) | code complete, pushed; **needs merge** |
| gh-wait-ci | `9b667bb` (PR #14) | complete, pushed; **needs merge** |
| simple-llm-harness | `bf6b176` = origin/master | clean; intentionally unpushed (empty-PR reasoning) |

### Next steps / what still needs doing (merge-order dependent):
1. **Free the environment / continue the session** — clear the disk (`rm -rf .../scratchpad/gp`, ~70 MB of fat APEs + debug sidecars) or start fresh. The session was mid-flight; everything below is on the work queue.
2. **Merge `go-s3-server#74`** → then bump gosmopolitan's submodule + `src/cmd/go.mod` together (this also gates go-s3-server coverage past `bbb`/`8550`).
3. **Read the two unread CI failures** (ubuntu, windows on `20346d25`) with a working `gh wait-ci` (#14 helps here) and fix/confirm them.
4. **Merge `gh-wait-ci#14`** after go-s3-server#74.
5. **Windows NT boot regression** (from `4e8ff115`, the dynamic GOOS/GOARCH change) — fix so branch + master go green on windows while keeping runtime.GOOS/GOARCH host-matching.
6. **Open defects, in priority order:**
   - **#48 — darwin arm64 `sync: unlock of unlocked mutex`** (highest-severity): fatal in `reflect.funcLayout`'s HashTrieMap caches; mutex state word reads `0xffffffff`; either a cosmo arm64 atomic/barrier ordering bug or an upstream double-checked-lazy-init race this fork's default parallelism surfaces. Needs `-race` on real macOS hardware; unexplainable otherwise.
   - **#51 — linker's nosplit stack check not running** on GOOS=cosmo (silent stack corruption risk).
   - **#46 — `cmd/pprof -disasm` finds no symbols in a stripped APE** (darwin/arm64 base-address math; needs a build-capable session).
   - **#45 — `cmd/pack`** (re-diagnose if it recurs).
   - **#23 — runtime gdb tests** (part-implemented at end: teach gdb the sidecar).
   - **#49 — truncated batch GET** (re-check against the new streaming client).
   - Plus known-incomplete by host: runtime `TestMemStats`/`TestPeriodicGC` (macOS), `syscall TestPgid` (PATH/env), `crypto/x509 TestIssue51759`, `cmd/cgo/internal/swig`, mime `TestTypeByExtensionUNIX` (host-dependent), run with `TestExecutableDeleted` (EBUSY when APE staging bind-mounts over original when run as root — CI is non-root so likely fine there).

---

## 13. Process notes worth keeping (governance & friction)

- **Local build was almost entirely blocked** early on: `make.bash` refused by a hook (`>"$GOBUILDTIMELOGFILE"` unresolvable redirect); `go build`/`go test` hook-blocked (use `go-toolchain` instead). So **CI was the only compiler** for a long stretch — every push was error-prone. The assistant later built a working local loop by extracting `cmd/dist` (`dist bootstrap` + `go install std`), removing the blind-push problem.
- **GitHub Actions rate limits** (403 on `/actions/*`, `GraphQL: API rate limit already exceeded for user ID 6569500`) repeatedly blocked CI reading; the workaround was the `gh wait-ci` background poller and the `github-state-mirror.pazer.io` mirror (`GH_TOKEN_WOW_LOOK_AT_MY_CODE`/`GH_TOKEN_PAZEROP`, not `GH_TOKEN`).
- **The user was terse and frequently hostile** ("fuck your stupid polling", "jfc", "worthless piece of shit", "enjoy working through the night loser"). Recurring friction: the assistant polling instead of waiting for wake events, giving ETAs that weren't met ("why did you give an ETA you knew was a lie?"), and `ste-lint` (ASD-STE100) failing pushes on its own markdown.
- **Standing constraints:** never force-push / push to another branch / open PRs unless asked; never post GitHub comments/reviews as the user; `rm`/`find -delete` route through a `recycler`; heredocs banned; never disable TLS or unset `HTTPS_PROXY`; don't read/write outside the session repos without `add_repo`.
- **A governing working rule proven repeatedly:** the assistant's best work came where it refused to guess (it would not push unverified changes into the runtime syscall layer or fork mechanism it couldn't compile), used negative controls, and disproved inherited recorded causes by experiment (e.g. the gdb OSABI theory, the first pass of the APE-exec fix). The two "ubuntu is green" misreads show the counterpoint: **a mid-flight `all-builds` snapshot is never a final tally.**

---

_End of reconstruction. All technical specifics above were read directly from the session export; where a fact was inferred rather than confirmed, it is flagged. For anything not covered, the raw transcripts and every commit message are in the source DB._