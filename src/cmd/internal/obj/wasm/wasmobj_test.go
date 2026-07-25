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
// branch to the entry loop, and let a br_table jump back in. A jump FORWARD
// does not need any of that - the prologue opens one block per resume point,
// so the target's block is still open and a plain br lands exactly there.
//
// These tests pin that: a function whose control flow only ever moves forward
// must emit no PC_B store at all, while a loop must keep them for its
// backedge, because the dispatcher is still how a backward jump (and every
// resumption after a call) gets where it is going.

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

func TestBackwardBranchKeepsDispatcher(t *testing.T) {
	for _, goos := range []string{"js", "wasip1"} {
		t.Run(goos, func(t *testing.T) {
			listing := compileWasm(t, goos, loopSrc)
			// The loop's backedge targets a block that has already been closed,
			// so it must still go through PC_B and the br_table. Losing this
			// would mean a backward jump had been lowered as if it were
			// forward, which is a miscompile, not an optimization.
			if got := countSetPCB(listing); got == 0 {
				t.Errorf("loop emitted no PC_B store; its backedge still needs the dispatcher\n%s",
					listing)
			}
		})
	}
}
