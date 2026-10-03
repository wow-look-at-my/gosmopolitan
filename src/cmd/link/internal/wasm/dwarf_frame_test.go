// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"debug/dwarf"
	"internal/testenv"
	"os"
	"path/filepath"
	"testing"

	objfilepkg "cmd/internal/objfile"
)

// wasmFrameBaseProg is built for wasm by TestWasmDwarfFrameBase. It is
// compiled with -N -l so every named local has a stack home.
const wasmFrameBaseProg = `package main

//go:noinline
func compute(a, b int64) int64 {
	x := a*3 + 1
	y := x + b
	z := x ^ y
	println("compute", x, y, z)
	return z
}

//go:noinline
func mix(p, q int64) int64 {
	u := p + q*7
	v := u * u
	println("mix", u, v)
	return v - p
}

func main() {
	println(compute(6, 7))
	println(mix(3, 5))
}
`

// DWARF expression opcodes used below (cmd/internal/dwarf is not
// imported to keep the test self-describing about the on-disk bytes).
const (
	opWASMLocation = 0xed // DW_OP_WASM_location, WebAssembly tool conventions
	opPlusUconst   = 0x23 // DW_OP_plus_uconst
	opFbreg        = 0x91 // DW_OP_fbreg
)

// TestWasmDwarfFrameBase checks that wasm subprograms carry an
// evaluable DW_AT_frame_base: wasm emits no .debug_frame, so instead of
// DW_OP_call_frame_cfa the frame base must compute the CFA directly as
// the value of wasm global 0 (the Go SP) plus framesize+8. Variable and
// formal-parameter DIEs must then carry DW_OP_fbreg locations against
// it: params at nonnegative offsets (the first at exactly fbreg 0,
// which used to be emitted as a bare, unevaluable DW_OP_call_frame_cfa)
// and stack locals at negative offsets.
func TestWasmDwarfFrameBase(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	if testing.Short() {
		t.Skip("skipping in short mode: cross-compiles the standard library for wasm")
	}
	t.Parallel()
	for _, goos := range []string{"js", "wasip1"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			testWasmDwarfFrameBase(t, goos)
		})
	}
}

func testWasmDwarfFrameBase(t *testing.T, goos string) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(wasmFrameBaseProg), 0666); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.wasm")
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-gcflags=all=-N -l", "-o", dst, src)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Logf("build: %s\n", b)
		t.Fatalf("build error: %v", err)
	}

	f, err := objfilepkg.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatalf("reading DWARF: %v", err)
	}

	// Per function: the variables we must see, and whether each is a
	// formal parameter (nonnegative fbreg offset) or a stack local
	// (negative fbreg offset).
	type varClass = bool
	const (
		param = varClass(true)
		local = varClass(false)
	)
	want := map[string]map[string]varClass{
		"main.compute": {"a": param, "b": param, "x": local, "y": local, "z": local},
		"main.mix":     {"p": param, "q": param, "u": local, "v": local},
	}
	firstParam := map[string]string{"main.compute": "a", "main.mix": "p"}

	r := d.Reader()
	var cur string // name of the wanted subprogram being walked, if any
	seen := make(map[string]map[string]bool)
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			break
		}
		switch e.Tag {
		case dwarf.TagSubprogram:
			name, _ := e.Val(dwarf.AttrName).(string)
			if _, ok := want[name]; !ok {
				cur = ""
				continue
			}
			cur = name
			seen[name] = make(map[string]bool)

			fb, ok := e.Val(dwarf.AttrFrameBase).([]byte)
			if !ok {
				t.Errorf("%s: no block-form DW_AT_frame_base", name)
				continue
			}
			// DW_OP_WASM_location 0x01 (wasm global) 0x00 (SP),
			// DW_OP_plus_uconst ULEB(framesize+8).
			if len(fb) < 5 || fb[0] != opWASMLocation || fb[1] != 0x01 || fb[2] != 0x00 || fb[3] != opPlusUconst {
				t.Errorf("%s: DW_AT_frame_base = %#x, want DW_OP_WASM_location 0x01 0x00 DW_OP_plus_uconst ...", name, fb)
				continue
			}
			addend, n := decodeUleb(fb[4:])
			if n != len(fb)-4 {
				t.Errorf("%s: trailing bytes after frame base ULEB: %#x", name, fb)
			}
			// The addend is framesize+8; these functions have nonempty
			// frames, so it must exceed the bare 8-byte return address.
			if addend <= 8 {
				t.Errorf("%s: frame base addend = %d, want > 8 (framesize+8)", name, addend)
			}

		case dwarf.TagFormalParameter, dwarf.TagVariable:
			if cur == "" {
				continue
			}
			name, _ := e.Val(dwarf.AttrName).(string)
			isParam, ok := want[cur][name]
			if !ok {
				continue
			}
			seen[cur][name] = true
			loc, ok := e.Val(dwarf.AttrLocation).([]byte)
			if !ok || len(loc) == 0 {
				t.Errorf("%s.%s: no location expression", cur, name)
				continue
			}
			if loc[0] != opFbreg {
				t.Errorf("%s.%s: location %#x does not begin with DW_OP_fbreg", cur, name, loc)
				continue
			}
			off, n := decodeSleb(loc[1:])
			if n != len(loc)-1 {
				t.Errorf("%s.%s: trailing bytes after fbreg SLEB: %#x", cur, name, loc)
			}
			if isParam && off < 0 {
				t.Errorf("%s.%s: param fbreg offset = %d, want >= 0", cur, name, off)
			}
			if !isParam && off >= 0 {
				t.Errorf("%s.%s: local fbreg offset = %d, want < 0", cur, name, off)
			}
			// The first stack parameter sits at exactly the CFA. It
			// used to be emitted as a bare DW_OP_call_frame_cfa, which
			// is unevaluable on wasm; it must now be DW_OP_fbreg 0.
			if name == firstParam[cur] && off != 0 {
				t.Errorf("%s.%s: first param fbreg offset = %d, want 0", cur, name, off)
			}
		}
	}

	for fn, vars := range want {
		got, ok := seen[fn]
		if !ok {
			t.Errorf("no DW_TAG_subprogram for %s", fn)
			continue
		}
		for v := range vars {
			if !got[v] {
				t.Errorf("%s: no DIE for variable %q", fn, v)
			}
		}
	}
}

func decodeUleb(b []byte) (v uint64, n int) {
	var shift uint
	for i, c := range b {
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, i + 1
		}
		shift += 7
	}
	return 0, 0
}

func decodeSleb(b []byte) (v int64, n int) {
	var shift uint
	for i, c := range b {
		v |= int64(c&0x7f) << shift
		shift += 7
		if c&0x80 == 0 {
			if c&0x40 != 0 && shift < 64 {
				v |= -1 << shift
			}
			return v, i + 1
		}
	}
	return 0, 0
}
