// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Parsing of WebAssembly modules (GOARCH=wasm), as emitted by
// cmd/link/internal/wasm.
//
// A Go wasm binary has no traditional text segment or symbol table.
// Functions live in the module's code section, and the linker gives
// function i the "address" (PC_F) funcValueOffset+i, with the runtime
// PC being PC_F<<16 | PC_B where PC_B is an intra-function resume
// point counter, not a byte offset (see cmd/link/internal/wasm/asm.go).
//
// This package instead exposes each function at its byte extent within
// the module file: Sym.Addr is the file offset of the function's code
// section body and Sym.Size is the body's length in bytes. That gives
// tools like objdump a linear, byte-addressed view that matches the
// actual encoded instructions. PCToLine accepts both these file
// offsets and runtime PCs (which start at funcValueOffset<<16, far
// above any reasonable file offset) and resolves them at function
// granularity via the pclntab, which is located inside the data
// section by scanning the reconstructed memory image for its magic
// number.

package objfile

import (
	"bytes"
	"debug/dwarf"
	"debug/gosym"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

const (
	// wasmFuncValueOffset is the offset between function indices and their
	// PC_F values. It mirrors funcValueOffset in cmd/link/internal/wasm.
	wasmFuncValueOffset = 0x1000

	// wasmMinPC is the lowest runtime PC: PC_F<<16 for the first function.
	// Addresses below it are treated as file offsets, addresses at or
	// above it as runtime PCs.
	wasmMinPC = wasmFuncValueOffset << 16
)

// wasm section IDs.
const (
	wasmSecCustom   = 0
	wasmSecType     = 1
	wasmSecImport   = 2
	wasmSecFunction = 3
	wasmSecTable    = 4
	wasmSecMemory   = 5
	wasmSecGlobal   = 6
	wasmSecExport   = 7
	wasmSecStart    = 8
	wasmSecElement  = 9
	wasmSecCode     = 10
	wasmSecData     = 11
)

var wasmMagic = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} // "\0asm" version 1

// WasmInfo describes WebAssembly-specific structure of an executable
// needed to symbolize a disassembly.
type WasmInfo struct {
	Imports   []string // module.name of each imported function, in index order
	Types     []string // rendered signature of each entry in the type section
	FuncTypes []uint32 // type index of each function, in code section order
}

// WasmInfo returns WebAssembly-specific information about the file,
// or nil if the entry is not a wasm module.
func (e *Entry) WasmInfo() *WasmInfo {
	f, ok := e.raw.(*wasmFile)
	if !ok {
		return nil
	}
	return &WasmInfo{
		Imports:   f.imports,
		Types:     f.types,
		FuncTypes: f.funcTypes,
	}
}

type wasmBody struct {
	off  uint64 // file offset of the function body
	size uint64 // length of the body in bytes
}

type wasmSeg struct {
	addr uint64 // linear memory address
	data []byte
}

type wasmFile struct {
	data      []byte   // entire module contents
	imports   []string // module.name per imported function
	types     []string // rendered function signatures
	funcTypes []uint32 // type index per code entry
	code      []wasmBody
	codeOff   uint64            // file offset of the code section's contents
	names     map[uint64]string // function index -> name, from the "name" custom section
	segs      []wasmSeg         // active data segments
	debug     map[string][]byte // ".debug_*" custom sections

	pclnOnce sync.Once
	tab      *gosym.Table // may be nil if the pclntab was not found
	pclntab  []byte
	pclnErr  error
}

func openWasm(r io.ReaderAt) (rawFile, error) {
	data, err := io.ReadAll(io.NewSectionReader(r, 0, 1<<62))
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, wasmMagic) {
		return nil, fmt.Errorf("not a wasm module")
	}
	f := &wasmFile{data: data}
	if err := f.parse(); err != nil {
		return nil, err
	}
	return f, nil
}

// A wasmReader reads the primitives of the wasm binary encoding.
// The first encoding error is latched: subsequent reads return zero
// values, so sequences of reads only need a single error check.
type wasmReader struct {
	data []byte
	off  int
	err  error
}

func (r *wasmReader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("offset %#x: %s", r.off, fmt.Sprintf(format, args...))
	}
}

func (r *wasmReader) len() int {
	if r.err != nil {
		return 0
	}
	return len(r.data) - r.off
}

func (r *wasmReader) byte() byte {
	if r.err != nil {
		return 0
	}
	if r.off >= len(r.data) {
		r.fail("unexpected EOF")
		return 0
	}
	b := r.data[r.off]
	r.off++
	return b
}

func (r *wasmReader) bytes(n uint64) []byte {
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.data)-r.off) {
		r.fail("unexpected EOF reading %d bytes", n)
		return nil
	}
	b := r.data[r.off : r.off+int(n)]
	r.off += int(n)
	return b
}

func (r *wasmReader) uleb() uint64 {
	var v uint64
	var shift uint
	for {
		b := r.byte()
		if r.err != nil {
			return 0
		}
		if shift == 63 && b > 1 {
			r.fail("uleb128 overflows uint64")
			return 0
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v
		}
		shift += 7
		if shift > 63 {
			r.fail("uleb128 too long")
			return 0
		}
	}
}

func (r *wasmReader) sleb() int64 {
	var v int64
	var shift uint
	for {
		b := r.byte()
		if r.err != nil {
			return 0
		}
		v |= int64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			if shift < 64 && b&0x40 != 0 {
				v |= -1 << shift
			}
			return v
		}
		if shift > 63 {
			r.fail("sleb128 too long")
			return 0
		}
	}
}

func (r *wasmReader) name() string {
	n := r.uleb()
	return string(r.bytes(n))
}

// skipLimits skips a limits encoding (used by table and memory types).
func (r *wasmReader) skipLimits() {
	flags := r.uleb()
	r.uleb() // min
	if flags&1 != 0 {
		r.uleb() // max
	}
}

// skipConstExpr skips a constant expression, returning the value if it
// is a plain i32.const or i64.const.
func (r *wasmReader) skipConstExpr() (val int64, isConst bool) {
	switch op := r.byte(); op {
	case 0x41: // i32.const
		val, isConst = r.sleb(), true
	case 0x42: // i64.const
		val, isConst = r.sleb(), true
	case 0x43: // f32.const
		r.bytes(4)
	case 0x44: // f64.const
		r.bytes(8)
	case 0x23, 0xd2: // global.get, ref.func
		r.uleb()
	case 0xd0: // ref.null
		r.byte()
	default:
		r.fail("unsupported constant expression opcode %#x", op)
		return 0, false
	}
	if end := r.byte(); r.err == nil && end != 0x0b {
		r.fail("constant expression not terminated by end (got %#x)", end)
	}
	return val, isConst && r.err == nil
}

func wasmValType(t byte) string {
	switch t {
	case 0x7f:
		return "i32"
	case 0x7e:
		return "i64"
	case 0x7d:
		return "f32"
	case 0x7c:
		return "f64"
	case 0x7b:
		return "v128"
	case 0x70:
		return "funcref"
	case 0x6f:
		return "externref"
	}
	return fmt.Sprintf("0x%02x", t)
}

func (f *wasmFile) parse() error {
	r := &wasmReader{data: f.data, off: len(wasmMagic)}
	for r.err == nil && r.len() > 0 {
		id := r.byte()
		size := r.uleb()
		start := r.off
		r.bytes(size)
		if r.err != nil {
			break
		}
		// A sub-reader restricted to the section's payload, but reading
		// at file-absolute offsets so that code body extents can be
		// recorded directly.
		sec := &wasmReader{data: f.data[:start+int(size)], off: start}
		switch id {
		case wasmSecType:
			f.parseTypes(sec)
		case wasmSecImport:
			f.parseImports(sec)
		case wasmSecFunction:
			f.parseFunctions(sec)
		case wasmSecCode:
			f.codeOff = uint64(start)
			f.parseCode(sec)
		case wasmSecData:
			f.parseData(sec)
		case wasmSecCustom:
			switch name := sec.name(); {
			case name == "name":
				f.parseNames(sec)
			case strings.HasPrefix(name, ".debug_"):
				if f.debug == nil {
					f.debug = make(map[string][]byte)
				}
				f.debug[name] = f.data[sec.off : start+int(size)]
			}
		}
		if sec.err != nil {
			return fmt.Errorf("parsing wasm section %d: %w", id, sec.err)
		}
	}
	if r.err != nil {
		return r.err
	}
	if f.code == nil {
		return fmt.Errorf("wasm module has no code section")
	}
	return nil
}

func (f *wasmFile) parseTypes(r *wasmReader) {
	n := r.uleb()
	for i := uint64(0); i < n && r.err == nil; i++ {
		if b := r.byte(); r.err == nil && b != 0x60 {
			r.fail("bad functype tag %#x", b)
			return
		}
		var buf bytes.Buffer
		buf.WriteByte('(')
		np := r.uleb()
		for j := uint64(0); j < np && r.err == nil; j++ {
			if j > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(wasmValType(r.byte()))
		}
		buf.WriteString(") -> (")
		nr := r.uleb()
		for j := uint64(0); j < nr && r.err == nil; j++ {
			if j > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(wasmValType(r.byte()))
		}
		buf.WriteByte(')')
		f.types = append(f.types, buf.String())
	}
}

func (f *wasmFile) parseImports(r *wasmReader) {
	n := r.uleb()
	for i := uint64(0); i < n && r.err == nil; i++ {
		module := r.name()
		field := r.name()
		switch kind := r.byte(); kind {
		case 0x00: // function
			r.uleb() // type index
			f.imports = append(f.imports, module+"."+field)
		case 0x01: // table
			r.byte() // reftype
			r.skipLimits()
		case 0x02: // memory
			r.skipLimits()
		case 0x03: // global
			r.byte() // valtype
			r.byte() // mutability
		default:
			r.fail("unknown import kind %d", kind)
		}
	}
}

func (f *wasmFile) parseFunctions(r *wasmReader) {
	n := r.uleb()
	for i := uint64(0); i < n && r.err == nil; i++ {
		f.funcTypes = append(f.funcTypes, uint32(r.uleb()))
	}
}

func (f *wasmFile) parseCode(r *wasmReader) {
	n := r.uleb()
	for i := uint64(0); i < n && r.err == nil; i++ {
		size := r.uleb()
		off := uint64(r.off)
		r.bytes(size)
		if r.err == nil {
			f.code = append(f.code, wasmBody{off: off, size: size})
		}
	}
}

func (f *wasmFile) parseData(r *wasmReader) {
	n := r.uleb()
	for i := uint64(0); i < n && r.err == nil; i++ {
		flags := r.uleb()
		switch flags {
		case 0, 2: // active segment (2: with explicit memory index)
			if flags == 2 {
				r.uleb() // memory index
			}
			addr, isConst := r.skipConstExpr()
			size := r.uleb()
			data := r.bytes(size)
			if r.err == nil && isConst {
				f.segs = append(f.segs, wasmSeg{addr: uint64(addr), data: data})
			}
		case 1: // passive segment
			size := r.uleb()
			r.bytes(size)
		default:
			r.fail("unknown data segment flags %d", flags)
		}
	}
}

// parseNames parses the "name" custom section. Errors are ignored:
// the section is a debugging aid, and the pclntab provides better
// names anyway.
func (f *wasmFile) parseNames(r *wasmReader) {
	names := make(map[uint64]string)
	for r.err == nil && r.len() > 0 {
		id := r.byte()
		size := r.uleb()
		payload := r.bytes(size)
		if r.err != nil {
			break
		}
		if id != 0x01 { // function names
			continue
		}
		sub := &wasmReader{data: payload}
		n := sub.uleb()
		for i := uint64(0); i < n && sub.err == nil; i++ {
			idx := sub.uleb()
			name := sub.name()
			if sub.err == nil {
				names[idx] = name
			}
		}
	}
	r.err = nil
	f.names = names
}

// funcIndex returns the index of the function whose body contains the
// given file offset, or -1.
func (f *wasmFile) funcIndex(off uint64) int {
	i := sort.Search(len(f.code), func(i int) bool { return off < f.code[i].off })
	if i == 0 {
		return -1
	}
	if b := f.code[i-1]; off < b.off+b.size {
		return i - 1
	}
	return -1
}

// pcln locates the pclntab inside the module's data section by
// reconstructing the initial linear memory image and scanning it for
// the pclntab magic number.
func (f *wasmFile) pcln() (textStart uint64, pclntab []byte, err error) {
	f.pclnOnce.Do(f.findPclntab)
	// The functab covers PC_F values starting at wasmFuncValueOffset
	// relative to a zero runtime.text (see textOff in
	// cmd/link/internal/ld/pcln.go: on wasm functab entries hold the
	// function index, not a byte offset).
	return 0, f.pclntab, f.pclnErr
}

func (f *wasmFile) findPclntab() {
	var max uint64
	for _, seg := range f.segs {
		if end := seg.addr + uint64(len(seg.data)); end > max {
			max = end
		}
	}
	if max == 0 || max > 1<<31 {
		f.pclnErr = fmt.Errorf("no usable wasm data section (memory image %d bytes)", max)
		return
	}
	mem := make([]byte, max)
	for _, seg := range f.segs {
		copy(mem[seg.addr:], seg.data)
	}

	// Scan for a plausible pclntab header: magic, two zero bytes,
	// pc quantum and pointer size (see debug/gosym). Verify each
	// candidate by actually parsing it.
	for _, magic := range [][]byte{
		{0xc1, 0xff, 0xff, 0xff, 0x00, 0x00}, // abi.CosmoPCLnTabMagic, what this fork writes
		{0xf1, 0xff, 0xff, 0xff, 0x00, 0x00}, // Go 1.20+
		{0xf0, 0xff, 0xff, 0xff, 0x00, 0x00}, // Go 1.18
		{0xfa, 0xff, 0xff, 0xff, 0x00, 0x00}, // Go 1.16
	} {
		for off := 0; ; {
			i := bytes.Index(mem[off:], magic)
			if i < 0 {
				break
			}
			pos := off + i
			off = pos + 1
			if pos+8 > len(mem) || mem[pos+6] != 1 || mem[pos+7] != 8 { // pc quantum, ptr size
				continue
			}
			tab, err := gosym.NewTable(nil, gosym.NewLineTable(mem[pos:], 0))
			if err != nil || len(tab.Funcs) == 0 {
				continue
			}
			// Entries must be function indices offset by wasmFuncValueOffset.
			first := tab.Funcs[0].Entry
			if first < wasmFuncValueOffset || first >= wasmFuncValueOffset+uint64(len(f.code)) {
				continue
			}
			f.tab = tab
			f.pclntab = mem[pos:]
			return
		}
	}
	f.pclnErr = fmt.Errorf("pclntab not found in wasm data section")
}

func (f *wasmFile) table() *gosym.Table {
	f.pclnOnce.Do(f.findPclntab)
	return f.tab
}

// PCToLine implements Liner. It resolves both file offsets of code
// section bytes (as used in Sym.Addr) and runtime PCs
// (PC_F<<16 | PC_B, as they appear in tracebacks), at function
// granularity.
func (f *wasmFile) PCToLine(pc uint64) (string, int, *gosym.Func) {
	tab := f.table()
	if tab == nil {
		return "", 0, nil
	}
	if pc >= wasmMinPC {
		pc >>= 16 // runtime PC -> function index (PC_F)
	} else {
		i := f.funcIndex(pc)
		if i < 0 {
			return "", 0, nil
		}
		pc = wasmFuncValueOffset + uint64(i)
	}
	return tab.PCToLine(pc)
}

func (f *wasmFile) symbols() ([]Sym, error) {
	tab := f.table()
	syms := make([]Sym, 0, len(f.imports)+len(f.code))
	for _, imp := range f.imports {
		syms = append(syms, Sym{Name: imp, Code: 'U'})
	}
	for i, body := range f.code {
		var name string
		if tab != nil {
			if fn := tab.PCToFunc(wasmFuncValueOffset + uint64(i)); fn != nil {
				name = fn.Name
			}
		}
		if name == "" && f.names != nil {
			name = f.names[uint64(len(f.imports)+i)]
		}
		if name == "" {
			name = fmt.Sprintf("f%d", len(f.imports)+i)
		}
		syms = append(syms, Sym{
			Name: name,
			Addr: body.off,
			Size: int64(body.size),
			Code: 'T',
		})
	}
	return syms, nil
}

// text exposes the whole module, addressed by file offset: the
// function bodies the symbols point at are not contiguous (each code
// section entry is preceded by its size), so the "text segment" is
// simply the file itself.
func (f *wasmFile) text() (textStart uint64, text []byte, err error) {
	return 0, f.data, nil
}

func (f *wasmFile) goarch() string {
	return "wasm"
}

func (f *wasmFile) loadAddress() (uint64, error) {
	return 0, nil
}

// DWARFCodeOffset returns the file offset of the code section's
// contents. DWARF code addresses in a wasm module are relative to this
// position: fileOffset = codeOffset + DWARF address. Sym.Addr values
// are file offsets, so Sym.Addr == DWARFCodeOffset() + DW_AT_low_pc
// for a function symbol.
func (e *Entry) DWARFCodeOffset() (uint64, bool) {
	f, ok := e.raw.(*wasmFile)
	if !ok {
		return 0, false
	}
	return f.codeOff, true
}

func (f *wasmFile) dwarf() (*dwarf.Data, error) {
	if f.debug[".debug_info"] == nil {
		return nil, fmt.Errorf("no DWARF data in wasm file")
	}
	get := func(name string) []byte { return f.debug[name] }
	d, err := dwarf.New(get(".debug_abbrev"), nil, nil, get(".debug_info"), get(".debug_line"), nil, get(".debug_ranges"), get(".debug_str"))
	if err != nil {
		return nil, err
	}
	for _, name := range []string{".debug_addr", ".debug_line_str", ".debug_str_offsets", ".debug_rnglists"} {
		if b := get(name); b != nil {
			if err := d.AddSection(name, b); err != nil {
				return nil, err
			}
		}
	}
	return d, nil
}
