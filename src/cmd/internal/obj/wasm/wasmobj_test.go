// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm_test

import (
	"internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Inter-block control flow on wasm normally round trips through the
// dispatcher at the top of the function: store the target block to PC_B,
// branch to the entry loop, and let a br_table jump back in. Two shapes do
// not need any of that.
//
// A jump FORWARD lands in a block the prologue has not closed yet, so a plain
// br gets there. And a jump BACK to the start of the region the code is
// already in is a loop, which wasm can express directly - a region can hold
// more than one basic block because a block nothing branches to no longer
// opens one, so an ordinary `for` loop's header and body sit in the same
// region and its backedge is a br to a real wasm loop.
//
// What must keep the dispatcher is a backward jump that crosses a region
// boundary, which is what a loop with a call in it has: the call's resume
// point splits the loop, and the runtime has to be able to re-enter there.

const forwardOnlySrc = `
package p

var sink int

//go:noinline
func f(a, b int) {
	if a > b {
		sink = a
		if a > 100 {
			sink = 100
		}
	} else if b > 50 {
		sink = b
	} else {
		sink = 0
	}
	sink++
}
`

const loopSrc = `
package p

var sink int

//go:noinline
func g(n int) {
	for i := 0; i < n; i++ {
		sink += i
	}
}
`

const loopWithCallSrc = `
package p

var sink int

//go:noinline
func ext(i int) int { return i*3 + 1 }

//go:noinline
func h(n int) {
	for i := 0; i < n; i++ {
		sink += ext(i)
	}
}
`

// compileWasm returns the -S listing for src built for GOOS/wasm.
func compileWasm(t *testing.T, goos, src string) string {
	t.Helper()
	testenv.MustHaveGoBuild(t)

	dir := t.TempDir()
	file := filepath.Join(dir, "p.go")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := testenv.Command(t, testenv.GoToolPath(t), "tool", "compile", "-S", "-p", "p",
		"-o", filepath.Join(dir, "p.o"), file)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile failed: %v\n%s", err, out)
	}
	return string(out)
}

// countSetPCB counts the dispatcher stores ("Set PC_B") in a -S listing.
func countSetPCB(listing string) int {
	n := 0
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "Set" && f[i+1] == "PC_B" {
				n++
				break
			}
		}
	}
	return n
}

// countOp counts instructions named op in a -S listing.
func countOp(listing, op string) int {
	n := 0
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		// A listing line is "0xADDR NNNNN (file:line) Op args...".
		for i, w := range f {
			if strings.HasPrefix(w, "(") && i+1 < len(f) {
				if f[i+1] == op {
					n++
				}
				break
			}
		}
	}
	return n
}

func TestForwardBranchesSkipDispatcher(t *testing.T) {
	for _, goos := range []string{"js", "wasip1"} {
		t.Run(goos, func(t *testing.T) {
			listing := compileWasm(t, goos, forwardOnlySrc)
			if got := countSetPCB(listing); got != 0 {
				t.Errorf("forward-only function emitted %d PC_B stores, want 0"+
					" (every jump in it moves forward, so none needs the dispatcher)\n%s",
					got, listing)
			}
		})
	}
}

func TestLoopBecomesRealLoop(t *testing.T) {
	for _, goos := range []string{"js", "wasip1"} {
		t.Run(goos, func(t *testing.T) {
			listing := compileWasm(t, goos, loopSrc)
			// One Loop is the entry point loop the dispatcher branches to; the
			// second is the counted loop itself. Without it the backedge would
			// be a store to PC_B and an indirect br_table jump per iteration.
			if got := countOp(listing, "Loop"); got < 2 {
				t.Errorf("counted loop compiled to %d Loop instructions, want at least 2"+
					" (the entry point loop plus the loop itself)\n%s", got, listing)
			}
		})
	}
}

func TestBackwardBranchAcrossRegionsKeepsDispatcher(t *testing.T) {
	for _, goos := range []string{"js", "wasip1"} {
		t.Run(goos, func(t *testing.T) {
			listing := compileWasm(t, goos, loopWithCallSrc)
			// The call in the body is a resume point, so the loop spans two
			// regions and its backedge targets a block that has already been
			// closed. Losing this would mean a backward jump had been lowered as
			// if its target were still open, which is a miscompile, not an
			// optimization.
			if got := countSetPCB(listing); got == 0 {
				t.Errorf("loop with a call in it emitted no PC_B store;"+
					" its backedge crosses a resume point and still needs the dispatcher\n%s",
					listing)
			}
		})
	}
}
