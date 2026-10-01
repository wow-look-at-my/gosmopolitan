# Loop-aware inlining (all targets)


Upstream's inliner is frequency-blind without a profile: a call on a cold error path and a call in a hot inner loop are judged. Loop bodies are where the nodes are, so "Go will not inline a function containing a loop" stayed true in practice long after the. This fork adds the static frequency estimate every other production compiler has (GCC divides growth by a frequency-weighted benefit, LLVM raises the threshold for call sites its block-frequency estimate calls hot, HotSpot C2 scales limits by call-site frequency), in `src/cmd/compile/internal/inline/loop.go`, for every GOOS/GOARCH.

**What ships on: loop nesting is acted on at the CALL SITE, not at the callee.** That split was decided by measurement, not taste.

- **Budget, call-site side** (on) - a call nested in a loop doubles the max callee cost per level (`inlineLoopSiteFactor`) to depth `inlineLoopMaxDepth` (3), ceiling `inlineLoopHotBudget`. A "big" caller's reduced limit of 20 grows the same way but never past 80.
- **Availability, callee side** (on) - the budget boost is useless if the callee never got an inline body, so a function this package calls from. This is the mechanism PGO uses for profile-hot functions, driven by static loop nesting instead, and like all inlinability it is decided when the.
- **Growth guard** (on) - one caller absorbs at most `inlineLoopGrowthBudget` (640) of extra cost from loop-boosted inlining, so inlining into a loop (which makes more of the caller loop-nested, which admits more inlining) cannot snowball.
- **Cost discount, callee side** (OFF by default, `-d=loopinlinediv=2` to enable) - charging a loop-nested node a fraction of its usual cost, bounded by `inlineLoopCostCredit`. This is the mechanism that most directly answers the original complaint, and on its own it measured as a net loss: it makes a.

Measured, all configurations interleaved round-robin so machine drift cancels (measured sequentially this box drifts +1.3% between the baseline and *itself* - larger than every effect here, and the reason the first three rounds of numbers were thrown away):

| | call site (shipped) | callee discount |
|---|---|---|
| nine whole-task workloads | **-1.1% median**, 6 faster / 1 slower | +1.1% median, 3 faster / 5 slower |
| hot loop calling loop helpers | **-2.3% median** (best -22.5%) | -2.3% median |
| text size (cmd/compile) | +5.00% | +1.76% |
| compiling std | no measurable change | no measurable change |

Over std the shipped default makes 1,489 more functions inlinable (15,888 -> 17,377) and inlines 4,967 more call sites (70,146 -> 75,113).

Every constant is overridable for re-tuning - `-d=loopinlinediv`, `loopinlinecredit`, `loopinlinefactor`, `loopinlinedepth`, `loopinlinebudget`, `loopinlinegrowth` (a negative value means zero) - and `-d=loopinline=0` restores upstream's decisions exactly, which is the bisect switch. Tests: `cmd/compile/internal/inline` (the cost/budget/allowance arithmetic) and `TestLoopInlining*` in `cmd/compile/internal/test` (end to end, all three polarities, one knob at a time). CI runs both plus the `testdir` inline/escape/live regress suites.

Two runtime annotations came with it, both fixing upstream inconsistencies that only surface once more code is inlined - an inlining change is a call-graph. The compiler-injected `unsafe.String` panic helpers (`runtime/unsafe.go`) and the 3-index `goPanicSlice3*` family plus `goPanicSliceConvert` (`runtime/panic.go`) are now `//go:yeswritebarrierrec`, matching the `unsafe.Slice` and 2-index `goPanicSlice*` helpers. Without that, any `//go:nowritebarrierrec` function reaching an inlined `unsafe.String` or a 3-index slice expression is rejected for a panic path that cannot occur. The `goPanicSlice3*` case is wasm-only in effect, since that is where bounds checks call these Go helpers rather than assembly `panicBounds*` - build `runtime` for.
