// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inline

import (
	"testing"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
)

func TestLoopSiteMaxCost(t *testing.T) {
	defer func(saved base.DebugFlags) { base.Debug = saved }(base.Debug)
	base.Debug.LoopInline = 1

	// Ordinary caller: the 80-node limit doubles per level of loop
	// nesting, up to the 320 ceiling, and stops growing past the
	// maximum depth.
	for _, tc := range []struct {
		limit, ceiling, depth, want int32
	}{
		{80, 320, 0, 80},
		{80, 320, 1, 160},
		{80, 320, 2, 320},
		{80, 320, 3, 320},
		{80, 320, 9, 320}, // clamped to inlineLoopMaxDepth
		{80, 320, -1, 80}, // not in a loop

		// Big caller: starts at the reduced 20-node limit and is held
		// under the ordinary budget however deep the nesting.
		{20, 80, 0, 20},
		{20, 80, 1, 40},
		{20, 80, 2, 80},
		{20, 80, 3, 80},

		// A ceiling below the limit never lowers it.
		{80, 40, 2, 80},
	} {
		if got := loopSiteMaxCost(tc.limit, tc.ceiling, tc.depth); got != tc.want {
			t.Errorf("loopSiteMaxCost(%d, %d, %d) = %d, want %d",
				tc.limit, tc.ceiling, tc.depth, got, tc.want)
		}
	}

	// Disabling loop-aware inlining pins every call site to the
	// unboosted limit.
	base.Debug.LoopInline = 0
	for depth := int32(0); depth < 4; depth++ {
		if got := loopSiteMaxCost(80, 320, depth); got != 80 {
			t.Errorf("loopinline=0: loopSiteMaxCost(80, 320, %d) = %d, want 80", depth, got)
		}
	}
}

func TestLoopTunables(t *testing.T) {
	defer func(saved base.DebugFlags) { base.Debug = saved }(base.Debug)
	base.Debug = base.DebugFlags{LoopInline: 1}

	// Unset means the built-in default.
	if got, want := loopCostDivisor(), int32(inlineLoopCostDivisor); got != want {
		t.Errorf("default loopCostDivisor() = %d, want %d", got, want)
	}
	// A positive value overrides it.
	base.Debug.LoopInlineDiv = 5
	if got := loopCostDivisor(); got != 5 {
		t.Errorf("loopCostDivisor() = %d, want 5", got)
	}
	// A negative value means zero, which turns the discount off without
	// disturbing the other two mechanisms.
	base.Debug.LoopInlineDiv = -1
	if got := loopCostDivisor(); got != 0 {
		t.Errorf("loopCostDivisor() = %d, want 0", got)
	}
	v := &hairyVisitor{loopCost: 1000}
	if got := v.loopDiscount(); got != 0 {
		t.Errorf("loopDiscount() with divisor off = %d, want 0", got)
	}

	// The discount is the loop-nested cost divided by the divisor, capped
	// by the credit.
	base.Debug.LoopInlineDiv = 2
	base.Debug.LoopInlineCredit = 40
	for _, tc := range []struct{ loopCost, want int32 }{
		{0, 0}, {10, 5}, {80, 40}, {1000, 40},
	} {
		v := &hairyVisitor{loopCost: tc.loopCost}
		if got := v.loopDiscount(); got != tc.want {
			t.Errorf("loopDiscount() with loopCost %d = %d, want %d", tc.loopCost, got, tc.want)
		}
	}
}

func TestLoopGrowthBudget(t *testing.T) {
	defer func(saved base.DebugFlags) { base.Debug = saved }(base.Debug)
	base.Debug = base.DebugFlags{LoopInline: 1, LoopInlineGrowth: 100}

	fn := new(ir.Func)
	defer delete(loopGrowth, fn)

	if !chargeLoopGrowth(fn, 60) {
		t.Fatal("first charge of 60 against a 100 allowance was refused")
	}
	if !chargeLoopGrowth(fn, 40) {
		t.Fatal("second charge of 40 against a 100 allowance was refused")
	}
	if chargeLoopGrowth(fn, 1) {
		t.Fatal("charge past the allowance was allowed")
	}
	// A free inline (one the ordinary rules already permitted) never
	// consumes the allowance.
	if !chargeLoopGrowth(fn, 0) {
		t.Fatal("zero charge was refused")
	}
}
