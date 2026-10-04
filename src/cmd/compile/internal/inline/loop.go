// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package inline

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
)

// Loop-aware inlining.

const (
	// inlineLoopCostDivisor is the rate at which nodes nested inside a loop are charged against the inlining budget.
	inlineLoopCostDivisor = 0

	// inlineLoopCostCredit caps the total discount a single function may receive from inlineLoopCostDivisor.
	inlineLoopCostCredit = inlineMaxBudget

	// inlineLoopSiteFactor is the factor by which the maximum acceptable callee cost grows per level of loop nesting at the call site.
	inlineLoopSiteFactor = 2
	inlineLoopMaxDepth   = 3

	// inlineLoopHotBudget is the analysis budget given to a function that is called from inside a loop.
	inlineLoopHotBudget = 4 * inlineMaxBudget

	// inlineLoopGrowthBudget is the total extra cost, beyond what the ordinary rules would have allowed.
	inlineLoopGrowthBudget = 8 * inlineMaxBudget
)

// The tunables above are the defaults; each can be overridden from the command line so that they can be swept.
func loopCostDivisor() int32  { return tunable(base.Debug.LoopInlineDiv, inlineLoopCostDivisor) }
func loopCostCredit() int32   { return tunable(base.Debug.LoopInlineCredit, inlineLoopCostCredit) }
func loopSiteFactor() int32   { return tunable(base.Debug.LoopInlineFactor, inlineLoopSiteFactor) }
func loopMaxDepth() int32     { return tunable(base.Debug.LoopInlineDepth, inlineLoopMaxDepth) }
func loopHotBudget() int32    { return tunable(base.Debug.LoopInlineBudget, inlineLoopHotBudget) }
func loopGrowthBudget() int32 { return tunable(base.Debug.LoopInlineGrowth, inlineLoopGrowthBudget) }

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

// loopScanCredit is the extra analysis budget the hairy visitor is given so
// that it can measure a loop-heavy function far enough.
func loopScanCredit() int32 {
	if !loopInlineEnabled() || loopCostDivisor() <= 0 {
		// No discount to earn, so there is nothing to keep measuring for: give up on an over-budget function exactly.
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
	// The ceiling bounds how far loop nesting may raise the limit.
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

// loopCallees holds the functions that some function in the package being compiled calls from inside a loop.
var loopCallees = make(map[*ir.Func]bool)

// loopGrowth tracks, per caller, how much loop-boosted inlining it has already absorbed.
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

// markLoopCallees records every function that funcs call from inside a loop,
// so that inlineBudget can give those callees a larger analysis budget.
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
