// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"debug/dwarf"
	"internal/testenv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	objfilepkg "cmd/internal/objfile"
)

// wasmDwarfProg is built for wasm by TestWasmDwarf. The tests depend
// on its exact line numbers.
const wasmDwarfProg = `package main

import "fmt"

var g = 0

//go:noinline
func alfa(x int) int { // line 8
	for i := 0; i < x; i++ { // line 9
		g += i // line 10
	}
	return g // line 12
}

//go:noinline
func bravo() { // line 16
	fmt.Println(alfa(10)) // line 17
}

func main() { // line 20
	bravo() // line 21
}
`

// TestWasmDwarf checks the DWARF embedded in a wasm module's custom
// sections: subprogram low/high PC values must be code-section-relative
// byte offsets matching the module's code section layout, and the line
// table must resolve function entries and mid-function statements to
// the right source lines.
func TestWasmDwarf(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	if testing.Short() {
		t.Skip("skipping in short mode: cross-compiles the standard library for wasm")
	}
	t.Parallel()
	for _, goos := range []string{"js", "wasip1"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			testWasmDwarf(t, goos)
		})
	}
}

func testWasmDwarf(t *testing.T, goos string) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(wasmDwarfProg), 0666); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.wasm")
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", dst, src)
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
	codeOff, ok := f.Entries()[0].DWARFCodeOffset()
	if !ok {
		t.Fatalf("no code section offset (not a wasm module?)")
	}

	// Sym.Addr for wasm functions is the file offset of the function
	// body; DWARF addresses are relative to the code section contents.
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	type extent struct{ addr, size uint64 }
	wantSyms := map[string]extent{
		"main.alfa":  {},
		"main.bravo": {},
		"main.main":  {},
	}
	for _, s := range syms {
		if _, ok := wantSyms[s.Name]; ok {
			wantSyms[s.Name] = extent{s.Addr, uint64(s.Size)}
		}
	}

	// Collect subprogram DIEs for the functions of interest and find
	// the "main" compilation unit.
	funcs := make(map[string]extent)
	var mainCU *dwarf.Entry
	r := d.Reader()
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			break
		}
		switch e.Tag {
		case dwarf.TagCompileUnit:
			if name, _ := e.Val(dwarf.AttrName).(string); name == "main" {
				mainCU = e
			}
		case dwarf.TagSubprogram:
			name, _ := e.Val(dwarf.AttrName).(string)
			if _, ok := wantSyms[name]; !ok {
				continue
			}
			lowpc, ok := e.Val(dwarf.AttrLowpc).(uint64)
			if !ok {
				t.Errorf("%s: no DW_AT_low_pc", name)
				continue
			}
			var highpc uint64
			switch v := e.Val(dwarf.AttrHighpc).(type) {
			case uint64: // address form
				highpc = v
			case int64: // constant form: offset from low pc
				highpc = lowpc + uint64(v)
			default:
				t.Errorf("%s: no DW_AT_high_pc", name)
				continue
			}
			funcs[name] = extent{lowpc, highpc - lowpc}
		}
	}
	if mainCU == nil {
		t.Fatal("no compilation unit named \"main\"")
	}

	for name, want := range wantSyms {
		got, ok := funcs[name]
		if !ok {
			t.Errorf("no DW_TAG_subprogram for %s", name)
			continue
		}
		if got.addr+codeOff != want.addr {
			t.Errorf("%s: DW_AT_low_pc = %#x, want %#x (Sym.Addr %#x - code section offset %#x)",
				name, got.addr, want.addr-codeOff, want.addr, codeOff)
		}
		if got.size != want.size {
			t.Errorf("%s: high-low = %#x, want body size %#x", name, got.size, want.size)
		}
	}

	// Check the line table: function entry rows must point at the
	// declaration lines, and some statement row inside main.alfa must
	// map to the loop body (line 10).
	lr, err := d.LineReader(mainCU)
	if err != nil {
		t.Fatal(err)
	}
	entryLine := map[string]int{"main.alfa": 8, "main.bravo": 16, "main.main": 20}
	for name, want := range entryLine {
		fn, ok := funcs[name]
		if !ok {
			continue
		}
		var le dwarf.LineEntry
		if err := lr.SeekPC(fn.addr, &le); err != nil {
			t.Errorf("%s: no line table row for entry pc %#x: %v", name, fn.addr, err)
			continue
		}
		if base := filepath.Base(le.File.Name); base != "main.go" || le.Line != want {
			t.Errorf("%s entry: line table says %s:%d, want main.go:%d", name, base, le.Line, want)
		}
	}

	// Scan all rows falling inside main.alfa. Every row must be in
	// main.go within the function's line extent, and line 10 (the loop
	// body, a statement in the middle of the function) must appear.
	alfa := funcs["main.alfa"]
	lr.Reset()
	sawLoopBody := false
	var le dwarf.LineEntry
	for {
		if err := lr.Next(&le); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if le.EndSequence || le.Address < alfa.addr || le.Address >= alfa.addr+alfa.size {
			continue
		}
		if base := filepath.Base(le.File.Name); base != "main.go" || le.Line < 8 || le.Line > 12 {
			t.Errorf("row inside main.alfa maps to %s:%d, want main.go:8..12", base, le.Line)
		}
		if le.Line == 10 && le.IsStmt {
			sawLoopBody = true
		}
	}
	if !sawLoopBody {
		t.Errorf("no statement row for main.go:10 inside main.alfa [%#x,%#x)", alfa.addr, alfa.addr+alfa.size)
	}

	// A -ldflags=-w build must carry no DWARF at all.
	dstw := filepath.Join(dir, "outw.wasm")
	cmd = testenv.Command(t, testenv.GoToolPath(t), "build", "-ldflags=-w", "-o", dstw, src)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Logf("build: %s\n", b)
		t.Fatalf("build error: %v", err)
	}
	fw, err := objfilepkg.Open(dstw)
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()
	if _, err := fw.DWARF(); err == nil || !strings.Contains(err.Error(), "no DWARF") {
		t.Errorf("-ldflags=-w build: DWARF() = %v, want no-DWARF error", err)
	}
}
