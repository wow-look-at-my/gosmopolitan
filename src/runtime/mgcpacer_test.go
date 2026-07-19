// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"fmt"
	"math"
	"math/rand"
	. "runtime"
	"testing"
	"time"
)

// TestGcPacer runs the pacer against an idealized simulation of a GC-heavy
// program and checks that trigger, goal overshoot, and utilization behave
// as modeled.
//
// This runtime's pacer diverges from upstream: trigger() clamps the runway
// to at least half the distance from the live heap to the goal, and the
// minimum-trigger ratio is ~0.5 (see minTriggerRatioNum in mgcpacer.go).
// Together these pin the trigger at (approximately) the midpoint between
// the live heap and the goal whenever the cons/mark-based runway estimate
// comes out smaller than half the headroom - which is every scenario below
// where background marking keeps up. The expectations therefore follow
// this model:
//
//   - triggerRatio = (trigger-live)/(goal-live) = 0.5 exactly.
//   - With that much runway the assist ratio stays below the point where
//     assists activate, so gcUtilization = GCBackgroundUtilization (0.25)
//     exactly, unless the allocation rate is high enough for assists to
//     bind (HeavyStepAlloc post-step, HeavyJitterAlloc).
//   - At u = 0.25, bytes are scanned:allocated at scanRate/(3*allocRate),
//     so the heap allocated during the mark phase is
//     A = S * 3*allocRate/scanRate for S bytes of scan work, and the peak
//     is trigger + A: goalRatio = (trigger + A)/goal, an intentional
//     undershoot of the goal. Each scenario's expected band below is
//     derived from that formula with the scenario's rates.
//
// The undershoot means GC cycles are more frequent than upstream's
// finish-at-the-goal pacing for the same GOGC: that is the deliberate
// throughput cost of guaranteeing mark phases real runway (bounded mark
// bursts); see the minTriggerRatioNum comment in mgcpacer.go.
func TestGcPacer(t *testing.T) {
	t.Parallel()

	const initialHeapBytes = 256 << 10
	for _, e := range []*gcExecTest{
		{
			// The most basic test case: a steady-state heap.
			// Growth to an O(MiB) heap, then constant heap size, alloc/scan rates.
			name:          "Steady",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n >= 25 {
					// At this alloc/scan rate, the pacer should be extremely close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
					// The min-runway clamp pins the trigger at the midpoint.
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					// goalRatio = (1.5 + A/M)/2 with A/M = k/(1+k),
					// k = 3*33/1024: modeled 0.794.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.77, 0.82)
				}
			},
		},
		{
			// Same as the steady-state case, but lots of stacks to scan relative to the heap size.
			name:          "SteadyBigStacks",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(132.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(2048).sum(ramp(128<<20, 8)),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				// Check the same conditions as the steady-state case.
				n := len(c)
				if n >= 25 {
					// At this alloc/scan rate, the pacer should be extremely
					// close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					// The big stack scan work inflates both the goal's non-heap
					// term and A (k = 3*132/1024 = 0.387 of the mostly-stack
					// scan work): modeled and observed ~0.84.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.81, 0.86)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
				}
			},
		},
		{
			// Same as the steady-state case, but lots of globals to scan relative to the heap size.
			name:          "SteadyBigGlobals",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  128 << 20,
			nCores:        8,
			allocRate:     constant(132.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				// Check the same conditions as the steady-state case.
				n := len(c)
				if n >= 25 {
					// At this alloc/scan rate, the pacer should be extremely
					// close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					// Same shape as SteadyBigStacks (the globals scan work
					// takes the place of the stack scan work).
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.81, 0.86)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
				}
			},
		},
		{
			// This tests the GC pacer's response to a small change in allocation rate.
			name:          "StepAlloc",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33.0).sum(ramp(66.0, 1).delay(50)),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        100,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if (n >= 25 && n < 50) || n >= 75 {
					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles
					// and then is able to settle again after a significant jump in allocation rate.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
				}
				if n >= 25 && n < 50 {
					// Pre-step: same as Steady (a=33, modeled 0.794).
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.77, 0.82)
				}
				if n >= 75 {
					// Post-step: a=99, k=3*99/1024, A/M = k/(1+k) = 0.225:
					// modeled (1.5+0.225)/2 = 0.862.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.84, 0.89)
				}
			},
		},
		{
			// This tests the GC pacer's response to a large change in allocation rate.
			name:          "HeavyStepAlloc",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33).sum(ramp(330, 1).delay(50)),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        100,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if (n >= 25 && n < 50) || n >= 75 {
					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles
					// and then is able to settle again after a significant jump in allocation rate.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
				}
				if n >= 25 && n < 50 {
					// Pre-step: same as Steady (a=33, modeled 0.794).
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.77, 0.82)
				}
				if n >= 75 {
					// Post-step: at a=363 the assist ratio binds (u > 0.25),
					// so the cycle finishes at the goal like upstream.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.95, 1.05)
				}
			},
		},
		{
			// This tests the GC pacer's response to a change in the fraction of the scannable heap.
			name:          "StepScannableFrac",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(128.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(0.2).sum(unit(0.5).delay(50)),
			stackBytes:    constant(8192),
			length:        100,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if (n >= 25 && n < 50) || n >= 75 {
					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles
					// and then is able to settle again after a significant jump in the scannable
					// fraction (note the step is a single-cycle spike: unit() emits one peak).
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					// a=128 at scannableFrac 0.2: A/M = 0.2*k/(1+0.2*k) with
					// k=3*128/1024: modeled 0.785.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.76, 0.81)
				}
			},
		},
		{
			// Tests the pacer for a high GOGC value with a large heap growth happening
			// in the middle. The purpose of the large heap growth is to check if GC
			// utilization ends up sensitive
			name:          "HighGOGC",
			gcPercent:     1500,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     random(7, 0x53).offset(165),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12), random(0.01, 0x1), unit(14).delay(25)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 12 {
					if n == 26 {
						// In the 26th cycle there's a heap growth. Overshoot is expected to maintain
						// a stable utilization, but we should *never* overshoot more than GOGC of
						// the next cycle.
						assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.50, 15)
					} else {
						// With GOGC=1500 the goal is ~16x the live heap and
						// the midpoint trigger lands at ~8.5x, so the cycle
						// finishes just past it: goalRatio ~ (8.5 + A/M)/16
						// with A/M = k/(1+k), k = 3*165/1024: modeled 0.55.
						assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.52, 0.60)
					}

					// Ensure utilization remains stable despite a growth in live heap size
					// at GC #25. This test fails prior to the GC pacer redesign.
					//
					// Because GOGC is so large, we should also be really close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, GCGoalUtilization+0.03)
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.03)
				}
			},
		},
		{
			// This test makes sure that in the face of a varying (in this case, oscillating) allocation
			// rate, the pacer does a reasonably good job of staying abreast of the changes.
			name:          "OscAlloc",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     oscillate(13, 0, 8).offset(67),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 12 {
					// After the 12th GC, the heap will stop growing. Now, just make sure that:
					// 1. Utilization isn't varying _too_ much, and
					// 2. The trigger stays pinned at the midpoint while the goal
					//    undershoot tracks the oscillating allocation rate:
					//    a in [54, 80] gives A/M in [0.137, 0.19], so goalRatio
					//    in ~[0.82, 0.85].
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.80, 0.87)
					assertInRange(t, "GC utilization", c[n-1].gcUtilization, 0.25, 0.3)
				}
			},
		},
		{
			// This test is the same as OscAlloc, but instead of oscillating, the allocation rate is jittery.
			name:          "JitterAlloc",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     random(13, 0xf).offset(132),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12), random(0.01, 0xe)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 12 {
					// After the 12th GC, the heap will stop growing. Now, just make sure that:
					// 1. Utilization isn't varying _too_ much, and
					// 2. The trigger stays pinned at the midpoint while the goal
					//    undershoot tracks the jittery allocation rate:
					//    a = 132+-13 gives A/M around 0.28, so goalRatio ~0.89
					//    (plus a little floating garbage from the growth jitter).
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.87, 0.93)
					assertInRange(t, "GC utilization", c[n-1].gcUtilization, 0.25, 0.275)
				}
			},
		},
		{
			// This test is the same as JitterAlloc, but with a much higher allocation rate.
			// The jitter is proportionally the same.
			name:          "HeavyJitterAlloc",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     random(33.0, 0x0).offset(330),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12), random(0.01, 0x152)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 13 {
					// After the 12th GC, the heap will stop growing. Now, just make sure that:
					// 1. Utilization isn't varying _too_ much, and
					// 2. The pacer is mostly keeping up with the goal.
					// We start at the 13th here because we want to use the 12th as a reference.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.95, 1.05)
					// Unlike the other tests, GC utilization here will vary more and tend higher.
					// Just make sure it's not going too crazy.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.05)
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[11].gcUtilization, 0.05)
				}
			},
		},
		{
			// This test sets a slow allocation rate and a small heap (close to the minimum heap size)
			// to try to minimize the difference between the trigger and the goal.
			name:          "SmallHeapSlowAlloc",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(1.0),
			scanRate:      constant(2048.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 3)),
			scannableFrac: constant(0.01),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 4 {
					// After the 4th GC, the heap will stop growing.
					// The heap sits well below the 4MB minimum heap goal, so the
					// midpoint trigger leaves a huge relative runway and the tiny
					// scan work (scannableFrac=0.01) finishes right at the
					// trigger: goalRatio ~ (live + 0.5*(goal-live))/goal, i.e.
					// 0.5 + 0.5*live/goal.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.52, 0.62)
					// The min-runway clamp pins the trigger at the midpoint even
					// on this tiny heap (no absolute floor is needed: with runway
					// exactly half the headroom the trigger lands exactly on the
					// minimum-trigger lower bound).
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
				}
				if n > 25 {
					// Double-check that GC utilization looks OK.

					// At this alloc/scan rate, the pacer should be extremely close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)
					// Make sure GC utilization has mostly levelled off.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.05)
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[11].gcUtilization, 0.05)
				}
			},
		},
		{
			// This test sets a slow allocation rate and a medium heap (around 10x the min heap size)
			// to try to minimize the difference between the trigger and the goal.
			name:          "MediumHeapSlowAlloc",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(1.0),
			scanRate:      constant(2048.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 8)),
			scannableFrac: constant(0.01),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 9 {
					// After the 9th GC, the heap will stop growing.
					// The heap is ~2x the minimum heap size, GOGC=100 makes the
					// goal ~2x live, and the tiny scan work finishes right at
					// the midpoint trigger: goalRatio ~ (1.5*live)/(2*live) =
					// 0.75.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.73, 0.77)
					// The min-runway clamp pins the trigger at the midpoint.
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
				}
				if n > 25 {
					// Double-check that GC utilization looks OK.

					// At this alloc/scan rate, the pacer should be extremely close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)
					// Make sure GC utilization has mostly levelled off.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.05)
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[11].gcUtilization, 0.05)
				}
			},
		},
		{
			// This test sets a slow allocation rate and a large heap to try to minimize the
			// difference between the trigger and the goal.
			name:          "LargeHeapSlowAlloc",
			gcPercent:     100,
			memoryLimit:   math.MaxInt64,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(1.0),
			scanRate:      constant(2048.0),
			growthRate:    constant(4.0).sum(ramp(-3.0, 12)),
			scannableFrac: constant(0.01),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 13 {
					// After the 13th GC, the heap will stop growing.
					// The tiny scan work finishes right at the midpoint
					// trigger: goalRatio ~ 0.75, like MediumHeapSlowAlloc.
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.73, 0.77)
					// The runway is proportional now (half the headroom, GBs on
					// this heap) rather than pinned near the minimum heap size,
					// so check the trigger ratio rather than an absolute runway.
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
				}
				if n > 25 {
					// Double-check that GC utilization looks OK.

					// At this alloc/scan rate, the pacer should be extremely close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)
					// Make sure GC utilization has mostly levelled off.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.05)
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[11].gcUtilization, 0.05)
				}
			},
		},
		{
			// The most basic test case with a memory limit: a steady-state heap.
			// Growth to an O(MiB) heap, then constant heap size, alloc/scan rates.
			// Provide a lot of room for the limit. Essentially, this should behave just like
			// the "Steady" test. Note that we don't simulate non-heap overheads, so the
			// memory limit and the heap limit are identical.
			name:          "SteadyMemoryLimit",
			gcPercent:     100,
			memoryLimit:   512 << 20,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if peak := c[n-1].heapPeak; peak >= applyMemoryLimitHeapGoalHeadroom(512<<20) {
					t.Errorf("peak heap size reaches heap limit: %d", peak)
				}
				if n >= 25 {
					// At this alloc/scan rate, the pacer should be extremely close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					// The limit leaves plenty of room, so this behaves like Steady.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.77, 0.82)
				}
			},
		},
		{
			// This is the same as the previous test, but gcPercent = -1, so the memory limit is
			// the only goal. The goal must saturate at the limit; the midpoint trigger means the
			// heap is collected halfway between the live heap and the limit-derived goal rather
			// than growing all the way to it.
			name:          "SteadyMemoryLimitNoGCPercent",
			gcPercent:     -1,
			memoryLimit:   512 << 20,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(2.0).sum(ramp(-1.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if goal := c[n-1].heapGoal; goal != applyMemoryLimitHeapGoalHeadroom(512<<20) {
					t.Errorf("heap goal is not the heap limit: %d", goal)
				}
				if n >= 25 {
					// At this alloc/scan rate, the pacer should be extremely close to the goal utilization.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					// The live heap is small relative to the limit-derived goal, so
					// goalRatio ~ 0.5 + 0.5*live/goal + A/goal, a little over 0.5.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.50, 0.56)
				}
			},
		},
		{
			// This test ensures that the pacer doesn't fall over even when the live heap exceeds
			// the memory limit. It also makes sure GC utilization actually rises to push back.
			name:          "ExceedMemoryLimit",
			gcPercent:     100,
			memoryLimit:   512 << 20,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(3.5).sum(ramp(-2.5, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 12 {
					// We're way over the memory limit, so we want to make sure our goal is set
					// as low as it possibly can be.
					if goal, live := c[n-1].heapGoal, c[n-1].heapLive; goal != live {
						t.Errorf("heap goal is not equal to live heap: %d != %d", goal, live)
					}
				}
				if n >= 25 {
					// Due to memory pressure, we should scale to 100% GC CPU utilization.
					// Note that in practice this won't actually happen because of the CPU limiter,
					// but it's not the pacer's job to limit CPU usage.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, 1.0, 0.005)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					// In this case, that just means it's not wavering around a whole bunch.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
				}
			},
		},
		{
			// Same as the previous test, but with gcPercent = -1.
			name:          "ExceedMemoryLimitNoGCPercent",
			gcPercent:     -1,
			memoryLimit:   512 << 20,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(3.5).sum(ramp(-2.5, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n < 10 {
					if goal := c[n-1].heapGoal; goal != applyMemoryLimitHeapGoalHeadroom(512<<20) {
						t.Errorf("heap goal is not the heap limit: %d", goal)
					}
				}
				if n > 12 {
					// We're way over the memory limit, so we want to make sure our goal is set
					// as low as it possibly can be.
					if goal, live := c[n-1].heapGoal, c[n-1].heapLive; goal != live {
						t.Errorf("heap goal is not equal to live heap: %d != %d", goal, live)
					}
				}
				if n >= 25 {
					// Due to memory pressure, we should scale to 100% GC CPU utilization.
					// Note that in practice this won't actually happen because of the CPU limiter,
					// but it's not the pacer's job to limit CPU usage.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, 1.0, 0.005)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					// In this case, that just means it's not wavering around a whole bunch.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
				}
			},
		},
		{
			// This test ensures that the pacer maintains the memory limit as the heap grows.
			name:          "MaintainMemoryLimit",
			gcPercent:     100,
			memoryLimit:   512 << 20,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(3.0).sum(ramp(-2.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if n > 12 {
					// We're trying to saturate the memory limit.
					if goal := c[n-1].heapGoal; goal != applyMemoryLimitHeapGoalHeadroom(512<<20) {
						t.Errorf("heap goal is not the heap limit: %d", goal)
					}
				}
				if n >= 25 {
					// At this alloc/scan rate, the pacer should be extremely close to the goal utilization,
					// even with the additional memory pressure.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					// The live heap (~390MB) sits close under the limit-derived goal
					// (~497MB), so the midpoint trigger leaves goalRatio ~
					// 0.5 + 0.5*live/goal + A/goal ~ 0.94.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.92, 0.97)
				}
			},
		},
		{
			// Same as the previous test, but with gcPercent = -1.
			name:          "MaintainMemoryLimitNoGCPercent",
			gcPercent:     -1,
			memoryLimit:   512 << 20,
			globalsBytes:  32 << 10,
			nCores:        8,
			allocRate:     constant(33.0),
			scanRate:      constant(1024.0),
			growthRate:    constant(3.0).sum(ramp(-2.0, 12)),
			scannableFrac: constant(1.0),
			stackBytes:    constant(8192),
			length:        50,
			checker: func(t *testing.T, c []gcCycleResult) {
				n := len(c)
				if goal := c[n-1].heapGoal; goal != applyMemoryLimitHeapGoalHeadroom(512<<20) {
					t.Errorf("heap goal is not the heap limit: %d", goal)
				}
				if n >= 25 {
					// At this alloc/scan rate, the pacer should be extremely close to the goal utilization,
					// even with the additional memory pressure.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, GCGoalUtilization, 0.005)

					// Make sure the pacer settles into a non-degenerate state in at least 25 GC cycles.
					// Same as MaintainMemoryLimit: the live heap sits close under
					// the limit-derived goal, so goalRatio ~ 0.94.
					assertInEpsilon(t, "GC utilization", c[n-1].gcUtilization, c[n-2].gcUtilization, 0.005)
					assertInRange(t, "trigger ratio", c[n-1].triggerRatio(), 0.495, 0.505)
					assertInRange(t, "goal ratio", c[n-1].goalRatio(), 0.92, 0.97)
				}
			},
		},
		// TODO(mknyszek): Write a test that exercises the pacer's hard goal.
		// This is difficult in the idealized model this testing framework places
		// the pacer in, because the calculated overshoot is directly proportional
		// to the runway for the case of the expected work.
		// However, it is still possible to trigger this case if something exceptional
		// happens between calls to revise; the framework just doesn't support this yet.
	} {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()

			c := NewGCController(e.gcPercent, e.memoryLimit)
			var bytesAllocatedBlackLast int64
			results := make([]gcCycleResult, 0, e.length)
			for i := 0; i < e.length; i++ {
				cycle := e.next()
				c.StartCycle(cycle.stackBytes, e.globalsBytes, cycle.scannableFrac, e.nCores)

				// Update pacer incrementally as we complete scan work.
				const (
					revisePeriod = 500 * time.Microsecond
					rateConv     = 1024 * float64(revisePeriod) / float64(time.Millisecond)
				)
				var nextHeapMarked int64
				if i == 0 {
					nextHeapMarked = initialHeapBytes
				} else {
					nextHeapMarked = int64(float64(int64(c.HeapMarked())-bytesAllocatedBlackLast) * cycle.growthRate)
				}
				globalsScanWorkLeft := int64(e.globalsBytes)
				stackScanWorkLeft := int64(cycle.stackBytes)
				heapScanWorkLeft := int64(float64(nextHeapMarked) * cycle.scannableFrac)
				doWork := func(work int64) (int64, int64, int64) {
					var deltas [3]int64

					// Do globals work first, then stacks, then heap.
					for i, workLeft := range []*int64{&globalsScanWorkLeft, &stackScanWorkLeft, &heapScanWorkLeft} {
						if *workLeft == 0 {
							continue
						}
						if *workLeft > work {
							deltas[i] += work
							*workLeft -= work
							work = 0
							break
						} else {
							deltas[i] += *workLeft
							work -= *workLeft
							*workLeft = 0
						}
					}
					return deltas[0], deltas[1], deltas[2]
				}
				var (
					gcDuration          int64
					assistTime          int64
					bytesAllocatedBlack int64
				)
				for heapScanWorkLeft+stackScanWorkLeft+globalsScanWorkLeft > 0 {
					// Simulate GC assist pacing.
					//
					// Note that this is an idealized view of the GC assist pacing
					// mechanism.

					// From the assist ratio and the alloc and scan rates, we can idealize what
					// the GC CPU utilization looks like.
					//
					// We start with assistRatio = (bytes of scan work) / (bytes of runway) (by definition).
					//
					// Over revisePeriod, we can also calculate how many bytes are scanned and
					// allocated, given some GC CPU utilization u:
					//
					//     bytesScanned   = scanRate  * rateConv * nCores * u
					//     bytesAllocated = allocRate * rateConv * nCores * (1 - u)
					//
					// During revisePeriod, assistRatio is kept constant, and GC assists kick in to
					// maintain it. Specifically, they act to prevent too many bytes being allocated
					// compared to how many bytes are scanned. It directly defines the ratio of
					// bytesScanned to bytesAllocated over this period, hence:
					//
					//     assistRatio = bytesScanned / bytesAllocated
					//
					// From this, we can solve for utilization, because everything else has already
					// been determined:
					//
					//     assistRatio = (scanRate * rateConv * nCores * u) / (allocRate * rateConv * nCores * (1 - u))
					//     assistRatio = (scanRate * u) / (allocRate * (1 - u))
					//     assistRatio * allocRate * (1-u) = scanRate * u
					//     assistRatio * allocRate - assistRatio * allocRate * u = scanRate * u
					//     assistRatio * allocRate = assistRatio * allocRate * u + scanRate * u
					//     assistRatio * allocRate = (assistRatio * allocRate + scanRate) * u
					//     u = (assistRatio * allocRate) / (assistRatio * allocRate + scanRate)
					//
					// Note that this may give a utilization that is _less_ than GCBackgroundUtilization,
					// which isn't possible in practice because of dedicated workers. Thus, this case
					// must be interpreted as GC assists not kicking in at all, and just round up. All
					// downstream values will then have this accounted for.
					assistRatio := c.AssistWorkPerByte()
					utilization := assistRatio * cycle.allocRate / (assistRatio*cycle.allocRate + cycle.scanRate)
					if utilization < GCBackgroundUtilization {
						utilization = GCBackgroundUtilization
					}

					// Knowing the utilization, calculate bytesScanned and bytesAllocated.
					bytesScanned := int64(cycle.scanRate * rateConv * float64(e.nCores) * utilization)
					bytesAllocated := int64(cycle.allocRate * rateConv * float64(e.nCores) * (1 - utilization))

					// Subtract work from our model.
					globalsScanned, stackScanned, heapScanned := doWork(bytesScanned)

					// doWork may not use all of bytesScanned.
					// In this case, the GC actually ends sometime in this period.
					// Let's figure out when, exactly, and adjust bytesAllocated too.
					actualElapsed := revisePeriod
					actualAllocated := bytesAllocated
					if actualScanned := globalsScanned + stackScanned + heapScanned; actualScanned < bytesScanned {
						// actualScanned = scanRate * rateConv * (t / revisePeriod) * nCores * u
						// => t = actualScanned * revisePeriod / (scanRate * rateConv * nCores * u)
						actualElapsed = time.Duration(float64(actualScanned) * float64(revisePeriod) / (cycle.scanRate * rateConv * float64(e.nCores) * utilization))
						actualAllocated = int64(cycle.allocRate * rateConv * float64(actualElapsed) / float64(revisePeriod) * float64(e.nCores) * (1 - utilization))
					}

					// Ask the pacer to revise.
					c.Revise(GCControllerReviseDelta{
						HeapLive:        actualAllocated,
						HeapScan:        int64(float64(actualAllocated) * cycle.scannableFrac),
						HeapScanWork:    heapScanned,
						StackScanWork:   stackScanned,
						GlobalsScanWork: globalsScanned,
					})

					// Accumulate variables.
					assistTime += int64(float64(actualElapsed) * float64(e.nCores) * (utilization - GCBackgroundUtilization))
					gcDuration += int64(actualElapsed)
					bytesAllocatedBlack += actualAllocated
				}

				// Put together the results, log them, and concatenate them.
				result := gcCycleResult{
					cycle:         i + 1,
					heapLive:      c.HeapMarked(),
					heapScannable: int64(float64(int64(c.HeapMarked())-bytesAllocatedBlackLast) * cycle.scannableFrac),
					heapTrigger:   c.Triggered(),
					heapPeak:      c.HeapLive(),
					heapGoal:      c.HeapGoal(),
					gcUtilization: float64(assistTime)/(float64(gcDuration)*float64(e.nCores)) + GCBackgroundUtilization,
				}
				t.Log("GC", result.String())
				results = append(results, result)

				// Run the checker for this test.
				e.check(t, results)

				c.EndCycle(uint64(nextHeapMarked+bytesAllocatedBlack), assistTime, gcDuration, e.nCores)

				bytesAllocatedBlackLast = bytesAllocatedBlack
			}
		})
	}
}

type gcExecTest struct {
	name string

	gcPercent    int
	memoryLimit  int64
	globalsBytes uint64
	nCores       int

	allocRate     float64Stream // > 0, KiB / cpu-ms
	scanRate      float64Stream // > 0, KiB / cpu-ms
	growthRate    float64Stream // > 0
	scannableFrac float64Stream // Clamped to [0, 1]
	stackBytes    float64Stream // Multiple of 2048.
	length        int

	checker func(*testing.T, []gcCycleResult)
}

// minRate is an arbitrary minimum for allocRate, scanRate, and growthRate.
// These values just cannot be zero.
const minRate = 0.0001

func (e *gcExecTest) next() gcCycle {
	return gcCycle{
		allocRate:     e.allocRate.min(minRate)(),
		scanRate:      e.scanRate.min(minRate)(),
		growthRate:    e.growthRate.min(minRate)(),
		scannableFrac: e.scannableFrac.limit(0, 1)(),
		stackBytes:    uint64(e.stackBytes.quantize(2048).min(0)()),
	}
}

func (e *gcExecTest) check(t *testing.T, results []gcCycleResult) {
	t.Helper()

	// Do some basic general checks first.
	n := len(results)
	switch n {
	case 0:
		t.Fatal("no results passed to check")
		return
	case 1:
		if results[0].cycle != 1 {
			t.Error("first cycle has incorrect number")
		}
	default:
		if results[n-1].cycle != results[n-2].cycle+1 {
			t.Error("cycle numbers out of order")
		}
	}
	if u := results[n-1].gcUtilization; u < 0 || u > 1 {
		t.Fatal("GC utilization not within acceptable bounds")
	}
	if s := results[n-1].heapScannable; s < 0 {
		t.Fatal("heapScannable is negative")
	}
	if e.checker == nil {
		t.Fatal("test-specific checker is missing")
	}

	// Run the test-specific checker.
	e.checker(t, results)
}

type gcCycle struct {
	allocRate     float64
	scanRate      float64
	growthRate    float64
	scannableFrac float64
	stackBytes    uint64
}

type gcCycleResult struct {
	cycle int

	// These come directly from the pacer, so uint64.
	heapLive    uint64
	heapTrigger uint64
	heapGoal    uint64
	heapPeak    uint64

	// These are produced by the simulation, so int64 and
	// float64 are more appropriate, so that we can check for
	// bad states in the simulation.
	heapScannable int64
	gcUtilization float64
}

func (r *gcCycleResult) goalRatio() float64 {
	return float64(r.heapPeak) / float64(r.heapGoal)
}

func (r *gcCycleResult) runway() float64 {
	return float64(r.heapGoal - r.heapTrigger)
}

func (r *gcCycleResult) triggerRatio() float64 {
	return float64(r.heapTrigger-r.heapLive) / float64(r.heapGoal-r.heapLive)
}

func (r *gcCycleResult) String() string {
	return fmt.Sprintf("%d %2.1f%% %d->%d->%d (goal: %d)", r.cycle, r.gcUtilization*100, r.heapLive, r.heapTrigger, r.heapPeak, r.heapGoal)
}

func assertInEpsilon(t *testing.T, name string, a, b, epsilon float64) {
	t.Helper()
	assertInRange(t, name, a, b-epsilon, b+epsilon)
}

func assertInRange(t *testing.T, name string, a, min, max float64) {
	t.Helper()
	if a < min || a > max {
		t.Errorf("%s not in range (%f, %f): %f", name, min, max, a)
	}
}

// float64Stream is a function that generates an infinite stream of
// float64 values when called repeatedly.
type float64Stream func() float64

// constant returns a stream that generates the value c.
func constant(c float64) float64Stream {
	return func() float64 {
		return c
	}
}

// unit returns a stream that generates a single peak with
// amplitude amp, followed by zeroes.
//
// In another manner of speaking, this is the Kronecker delta.
func unit(amp float64) float64Stream {
	dropped := false
	return func() float64 {
		if dropped {
			return 0
		}
		dropped = true
		return amp
	}
}

// oscillate returns a stream that oscillates sinusoidally
// with the given amplitude, phase, and period.
func oscillate(amp, phase float64, period int) float64Stream {
	var cycle int
	return func() float64 {
		p := float64(cycle)/float64(period)*2*math.Pi + phase
		cycle++
		if cycle == period {
			cycle = 0
		}
		return math.Sin(p) * amp
	}
}

// ramp returns a stream that moves from zero to height
// over the course of length steps.
func ramp(height float64, length int) float64Stream {
	var cycle int
	return func() float64 {
		h := height * float64(cycle) / float64(length)
		if cycle < length {
			cycle++
		}
		return h
	}
}

// random returns a stream that generates random numbers
// between -amp and amp.
func random(amp float64, seed int64) float64Stream {
	r := rand.New(rand.NewSource(seed))
	return func() float64 {
		return ((r.Float64() - 0.5) * 2) * amp
	}
}

// delay returns a new stream which is a buffered version
// of f: it returns zero for cycles steps, followed by f.
func (f float64Stream) delay(cycles int) float64Stream {
	zeroes := 0
	return func() float64 {
		if zeroes < cycles {
			zeroes++
			return 0
		}
		return f()
	}
}

// scale returns a new stream that is f, but attenuated by a
// constant factor.
func (f float64Stream) scale(amt float64) float64Stream {
	return func() float64 {
		return f() * amt
	}
}

// offset returns a new stream that is f but offset by amt
// at each step.
func (f float64Stream) offset(amt float64) float64Stream {
	return func() float64 {
		old := f()
		return old + amt
	}
}

// sum returns a new stream that is the sum of all input streams
// at each step.
func (f float64Stream) sum(fs ...float64Stream) float64Stream {
	return func() float64 {
		sum := f()
		for _, s := range fs {
			sum += s()
		}
		return sum
	}
}

// quantize returns a new stream that rounds f to a multiple
// of mult at each step.
func (f float64Stream) quantize(mult float64) float64Stream {
	return func() float64 {
		r := f() / mult
		if r < 0 {
			return math.Ceil(r) * mult
		}
		return math.Floor(r) * mult
	}
}

// min returns a new stream that replaces all values produced
// by f lower than min with min.
func (f float64Stream) min(min float64) float64Stream {
	return func() float64 {
		return math.Max(min, f())
	}
}

// max returns a new stream that replaces all values produced
// by f higher than max with max.
func (f float64Stream) max(max float64) float64Stream {
	return func() float64 {
		return math.Min(max, f())
	}
}

// limit returns a new stream that replaces all values produced
// by f lower than min with min and higher than max with max.
func (f float64Stream) limit(min, max float64) float64Stream {
	return func() float64 {
		v := f()
		if v < min {
			v = min
		} else if v > max {
			v = max
		}
		return v
	}
}

func applyMemoryLimitHeapGoalHeadroom(goal uint64) uint64 {
	headroom := goal / 100 * MemoryLimitHeapGoalHeadroomPercent
	if headroom < MemoryLimitMinHeapGoalHeadroom {
		headroom = MemoryLimitMinHeapGoalHeadroom
	}
	if goal < headroom || goal-headroom < headroom {
		goal = headroom
	} else {
		goal -= headroom
	}
	return goal
}

// TestGCControllerDonatedFractionalCap checks the donation-conditioned
// fractional worker cap: when the embedder has recently donated idle time
// via the budgeted mark step, startCycle caps the fractional utilization
// goal at DonatedFractionalUtilizationGoal; otherwise the standard quota
// applies.
func TestGCControllerDonatedFractionalCap(t *testing.T) {
	// gomaxprocs=1 puts the whole 25% background utilization goal on the
	// fractional worker, so the cap is observable.
	const procs = 1

	c := NewGCController(100, math.MaxInt64)
	c.StartCycle(2048, 32<<10, 1.0, procs)
	if got := c.FractionalUtilizationGoal(); !(got > DonatedFractionalUtilizationGoal) {
		t.Errorf("without donation, fractional goal = %f, want > %f (the standard quota)", got, DonatedFractionalUtilizationGoal)
	}

	c = NewGCController(100, math.MaxInt64)
	c.NoteMarkStepDonation()
	c.StartCycle(2048, 32<<10, 1.0, procs)
	if got := c.FractionalUtilizationGoal(); got != DonatedFractionalUtilizationGoal {
		t.Errorf("with recent donation, fractional goal = %f, want %f", got, DonatedFractionalUtilizationGoal)
	}

	// With enough procs the fractional worker isn't used at all
	// (fractional goal 0); the cap must not raise it.
	c = NewGCController(100, math.MaxInt64)
	c.NoteMarkStepDonation()
	c.StartCycle(2048, 32<<10, 1.0, 8)
	if got := c.FractionalUtilizationGoal(); got != 0 {
		t.Errorf("with 8 procs, fractional goal = %f, want 0 (dedicated workers only)", got)
	}
}

func TestIdleMarkWorkerCount(t *testing.T) {
	const workers = 10
	c := NewGCController(100, math.MaxInt64)
	c.SetMaxIdleMarkWorkers(workers)
	for i := 0; i < workers; i++ {
		if !c.NeedIdleMarkWorker() {
			t.Fatalf("expected to need idle mark workers: i=%d", i)
		}
		if !c.AddIdleMarkWorker() {
			t.Fatalf("expected to be able to add an idle mark worker: i=%d", i)
		}
	}
	if c.NeedIdleMarkWorker() {
		t.Fatalf("expected to not need idle mark workers")
	}
	if c.AddIdleMarkWorker() {
		t.Fatalf("expected to not be able to add an idle mark worker")
	}
	for i := 0; i < workers; i++ {
		c.RemoveIdleMarkWorker()
		if !c.NeedIdleMarkWorker() {
			t.Fatalf("expected to need idle mark workers after removal: i=%d", i)
		}
	}
	for i := 0; i < workers-1; i++ {
		if !c.AddIdleMarkWorker() {
			t.Fatalf("expected to be able to add idle mark workers after adding again: i=%d", i)
		}
	}
	for i := 0; i < 10; i++ {
		if !c.AddIdleMarkWorker() {
			t.Fatalf("expected to be able to add idle mark workers interleaved: i=%d", i)
		}
		if c.AddIdleMarkWorker() {
			t.Fatalf("expected to not be able to add idle mark workers interleaved: i=%d", i)
		}
		c.RemoveIdleMarkWorker()
	}
	// Support the max being below the count.
	c.SetMaxIdleMarkWorkers(0)
	if c.NeedIdleMarkWorker() {
		t.Fatalf("expected to not need idle mark workers after capacity set to 0")
	}
	if c.AddIdleMarkWorker() {
		t.Fatalf("expected to not be able to add idle mark workers after capacity set to 0")
	}
	for i := 0; i < workers-1; i++ {
		c.RemoveIdleMarkWorker()
	}
	if c.NeedIdleMarkWorker() {
		t.Fatalf("expected to not need idle mark workers after capacity set to 0")
	}
	if c.AddIdleMarkWorker() {
		t.Fatalf("expected to not be able to add idle mark workers after capacity set to 0")
	}
	c.SetMaxIdleMarkWorkers(1)
	if !c.NeedIdleMarkWorker() {
		t.Fatalf("expected to need idle mark workers after capacity set to 1")
	}
	if !c.AddIdleMarkWorker() {
		t.Fatalf("expected to be able to add idle mark workers after capacity set to 1")
	}
}
