// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inline

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
)

// Loop-aware inlining.
//
// Without a profile, Go's inliner is frequency-blind: a call on a cold
// error path and a call in the innermost loop of a hot kernel are judged
// by exactly the same size budget, and a callee is charged the same for a
// node whether that node runs once or a million times. That is why "Go
// won't inline a function containing a loop" has been true in practice
// long after the structural restriction was lifted: loop bodies are where
// the nodes are, so loops are what exhausts the 80-node budget.
//
// A loop is the strongest static predictor of execution frequency there
// is, and every other production compiler uses it as one: GCC's inliner
// divides a candidate's size growth by a frequency-weighted time benefit
// (predict.cc assumes a loop iterates several times), LLVM raises the
// threshold for call sites its block-frequency estimate says are hot, and
// HotSpot C2 scales its inlining limits by call-site frequency. This file
// gives cmd/compile the same static estimate, in three parts.
//
// What the measurements said, because it decided which parts ship on:
// loop nesting is worth acting on at the CALL SITE and not at the callee.
// Boosting the budget where the call runs many times (parts 2 and 3) is
// -1.1% median across nine whole-task workloads, 6 faster and 1 slower.
// Discounting a callee for containing a loop (part 1) is +1.1% median, 5
// of 9 slower, because it makes that callee cheap at cold call sites too
// and the growth lands where nothing is gained. Part 1 therefore defaults
// off. On code shaped like the original complaint - a hot loop calling
// helpers the flat budget rejects - the shipped default is -2.3% median
// with individual workloads up to -22%. All figures from runs that
// interleave configurations round-robin: measured sequentially, this box
// drifts +1.3% between the baseline and ITSELF, which is larger than
// every effect here.
//
// The three parts:
//
//  1. Cost, callee side (loopCost/loopDiscount below, applied by
//     hairyVisitor): nodes nested in a loop are charged a fraction of the
//     usual cost, bounded by inlineLoopCostCredit. A function whose body
//     is a loop is no longer disqualified just for being one, but a
//     function with an enormous loop body still is. OFF by default;
//     -d=loopinlinediv=2 enables it. See inlineLoopCostDivisor.
//
//  2. Budget, call-site side (loopSiteMaxCost): a call nested in a loop
//     accepts a more expensive callee, growing with loop depth.
//
//  3. Availability, callee side (markLoopCallees/isLoopHotFunc): part 2
//     is useless if the callee was never given an inline body to begin
//     with, so a function this package calls from inside a loop is
//     analyzed with a larger budget - the same mechanism PGO uses for
//     profile-hot functions, driven by static loop nesting instead.
//
// Unbounded, this is a code-size disaster: inlining into a loop makes the
// loop bigger, which makes more of the caller loop-nested, which admits
// more inlining. Every part is therefore individually capped, and part 2
// additionally draws from a per-caller growth allowance
// (inlineLoopGrowthBudget) so that a single function cannot absorb
// unlimited loop-boosted inlining.
//
// All of it can be turned off with -d=loopinline=0, which restores the
// previous decisions exactly.

const (
	// inlineLoopCostDivisor is the rate at which nodes nested inside a
	// loop are charged against the inlining budget: a loop-nested node
	// costs 1/inlineLoopCostDivisor of what the same node costs outside
	// a loop.
	//
	// DEFAULT OFF (0), on the measurements below. This is the mechanism
	// that most directly answers "Go won't inline a function containing
	// a loop", and measured on its own it is a net loss: it makes a loop
	// function cheap at EVERY call site, including the cold ones, so the
	// code growth lands everywhere while the benefit only materializes
	// where the call is hot. Over nine whole-task workloads (1.8MB JSON
	// encode/decode, deflate/inflate, regexp, sort, go/parser,
	// text/template), interleaved so machine drift cancels: +1.1% median,
	// 3 faster and 5 slower, for +1.76% text. The call-site mechanism
	// below buys -1.1% median (6 faster, 1 slower) instead.
	//
	// Turn it on with -d=loopinlinediv=2 for a workload built out of
	// small hot kernels, where it measured well.
	inlineLoopCostDivisor = 0

	// inlineLoopCostCredit caps the total discount a single function may
	// receive from inlineLoopCostDivisor, so that a function consisting
	// of one gigantic loop does not become free. With the default budget
	// of 80 and a credit of 80, the most expensive function that can be
	// inlined at an ordinary call site has a raw cost of 160.
	inlineLoopCostCredit = inlineMaxBudget

	// inlineLoopSiteFactor is the factor by which the maximum acceptable
	// callee cost grows per level of loop nesting at the call site, and
	// inlineLoopMaxDepth is the loop depth past which it stops growing.
	inlineLoopSiteFactor = 2
	inlineLoopMaxDepth   = 3

	// inlineLoopHotBudget is the analysis budget given to a function that
	// is called from inside a loop, and the ceiling on the cost a
	// loop-nested call site will accept. It is deliberately far below the
	// PGO budget (inlineHotMaxBudget): a static guess that a loop is hot
	// is much weaker evidence than a profile that says so.
	inlineLoopHotBudget = 4 * inlineMaxBudget

	// inlineLoopGrowthBudget is the total extra cost, beyond what the
	// ordinary rules would have allowed, that one function may absorb
	// from loop-boosted inlining.
	inlineLoopGrowthBudget = 8 * inlineMaxBudget
)

// The tunables above are the defaults; each can be overridden from the
// command line so that they can be swept and re-tuned against real
// programs rather than argued about. -d=loopinline=0 turns the whole
// thing off and restores the previous inlining decisions exactly.
func loopCostDivisor() int32  { return tunable(base.Debug.LoopInlineDiv, inlineLoopCostDivisor) }
func loopCostCredit() int32   { return tunable(base.Debug.LoopInlineCredit, inlineLoopCostCredit) }
func loopSiteFactor() int32   { return tunable(base.Debug.LoopInlineFactor, inlineLoopSiteFactor) }
func loopMaxDepth() int32     { return tunable(base.Debug.LoopInlineDepth, inlineLoopMaxDepth) }
func loopHotBudget() int32    { return tunable(base.Debug.LoopInlineBudget, inlineLoopHotBudget) }
func loopGrowthBudget() int32 { return tunable(base.Debug.LoopInlineGrowth, inlineLoopGrowthBudget) }

// tunable returns the command-line override v if one was given (-1 means
// "zero, explicitly"), and def otherwise.
func tunable(v, def int) int32 {
	switch {
	case v < 0:
		return 0
	case v > 0:
		return int32(v)
	}
	return int32(def)
}

// loopInlineEnabled reports whether loop-aware inlining is on.
func loopInlineEnabled() bool {
	return base.Debug.LoopInline != 0
}

// loopScanCredit is the extra analysis budget the hairy visitor is given
// so that it can measure a loop-heavy function far enough to find out
// whether the loop discount rescues it.
func loopScanCredit() int32 {
	if !loopInlineEnabled() || loopCostDivisor() <= 0 {
		// No discount to earn, so there is nothing to keep measuring
		// for: give up on an over-budget function exactly where the
		// unmodified compiler would.
		return 0
	}
	return loopCostCredit()
}

// loopDiscount returns the cost credit earned by v's loop-nested code.
func (v *hairyVisitor) loopDiscount() int32 {
	if !loopInlineEnabled() {
		return 0
	}
	div := loopCostDivisor()
	if div <= 0 {
		return 0
	}
	return min(v.loopCost/div, loopCostCredit())
}

// loopSiteMaxCost returns the maximum callee cost accepted at a call site
// nested at the given loop depth, starting from the depth-0 limit and
// never exceeding ceiling.
func loopSiteMaxCost(limit, ceiling, depth int32) int32 {
	if !loopInlineEnabled() || depth <= 0 {
		return limit
	}
	// The ceiling bounds how far loop nesting may raise the limit; it
	// never lowers what the call site would have accepted anyway.
	ceiling = max(ceiling, limit)
	depth = min(depth, loopMaxDepth())
	factor := loopSiteFactor()
	c := limit
	for range depth {
		if factor <= 1 || c > ceiling/factor {
			return min(c, ceiling)
		}
		c *= factor
	}
	return min(c, ceiling)
}

// loopCallees holds the functions that some function in the package being
// compiled calls from inside a loop. Like the PGO hot-callee set it feeds
// the analysis budget in inlineBudget, so that such a callee has an inline
// body available for the loop-nested call sites that want it.
var loopCallees = make(map[*ir.Func]bool)

// loopGrowth tracks, per caller, how much loop-boosted inlining it has
// already absorbed. A missing entry means the full allowance remains.
var loopGrowth = make(map[*ir.Func]int32)

// isLoopHotFunc reports whether fn is called from inside a loop somewhere
// in the package being compiled.
func isLoopHotFunc(fn *ir.Func) bool {
	return loopInlineEnabled() && loopCallees[fn]
}

// chargeLoopGrowth reports whether caller may absorb extra cost worth of
// loop-boosted inlining, deducting it from the caller's allowance if so.
func chargeLoopGrowth(caller *ir.Func, extra int32) bool {
	if extra <= 0 {
		return true
	}
	spent := loopGrowth[caller]
	if spent+extra > loopGrowthBudget() {
		return false
	}
	loopGrowth[caller] = spent + extra
	return true
}

// markLoopCallees records every function that funcs call from inside a
// loop, so that inlineBudget can give those callees a larger analysis
// budget.
func markLoopCallees(funcs []*ir.Func) {
	if !loopInlineEnabled() {
		return
	}
	for _, fn := range funcs {
		visitLoopDepth(fn, func(n ir.Node, depth int32) {
			if depth <= 0 {
				return
			}
			call, ok := n.(*ir.CallExpr)
			if !ok || call.Op() != ir.OCALLFUNC || call.GoDefer || call.NoInline {
				return
			}
			if callee := staticCallee(call.Fun); callee != nil {
				loopCallees[callee] = true
			}
		})
	}
}

// staticCallee returns the function that the call target expression fn
// statically refers to, if any. It is a subset of inlCallee, which cannot
// be used here because it has the side effect of running CanInline.
func staticCallee(fn ir.Node) *ir.Func {
	switch fn := ir.StaticValue(fn).(type) {
	case *ir.Name:
		if fn.Class == ir.PFUNC {
			return fn.Func
		}
	case *ir.SelectorExpr:
		if fn.Op() == ir.OMETHEXPR {
			if n := ir.MethodExprName(fn); n != nil {
				return n.Func
			}
		}
	case *ir.ClosureExpr:
		return fn.Func
	}
	return nil
}

// visitLoopDepth calls do for every node in fn's body, along with the
// number of loops enclosing that node.
//
// A loop's Init statements and a range statement's ranged expression run
// once rather than once per iteration; they are nonetheless reported at
// the inner depth. Doing better would mean hand-walking the fields of
// every loop node, and the difference is one expression per loop.
func visitLoopDepth(fn *ir.Func, do func(n ir.Node, depth int32)) {
	depth := int32(0)
	var visit func(ir.Node) bool
	visit = func(n ir.Node) bool {
		do(n, depth)
		if isLoopNode(n) {
			depth++
			ir.DoChildren(n, visit)
			depth--
			return false
		}
		return ir.DoChildren(n, visit)
	}
	ir.DoChildren(fn, visit)
}

// isLoopNode reports whether n is a loop statement, i.e. whether n's body
// may run many times per execution of n.
func isLoopNode(n ir.Node) bool {
	switch n.Op() {
	case ir.OFOR, ir.ORANGE:
		return true
	}
	return false
}
