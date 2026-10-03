// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Disassembly of WebAssembly function bodies.
//
// Unlike the other architectures, wasm has no x/arch decoder, and its
// instructions cannot be decoded statelessly: a function body starts
// with a vector of local declarations, control instructions nest, and
// symbolizing a call requires the module's function index space
// (imports followed by the code section functions). The Disasm methods
// therefore route wasm to the stateful decoder in this file instead of
// a disasmFunc.
//
// Addresses are file offsets of the encoded instructions inside the
// module (see cmd/internal/objfile/wasm.go), so the output lines up
// with what other wasm tooling reports. Mnemonics follow the wasm text
// format (i32.add, local.get, ...) rather than Go assembly names,
// since a linked module contains plain spec instructions.

package disasm

import (
	"fmt"
	"math"
	"strings"

	"cmd/internal/objfile"
)

// wasmCtx carries the module-level information needed to symbolize a
// wasm disassembly.
type wasmCtx struct {
	imports   []string // qualified names of imported functions
	funcNames []string // function names, in code section order
	funcAddrs []uint64 // body file offsets, in code section order
	funcSizes []uint64 // body sizes in bytes
	types     []string // rendered signatures per type index
	funcTypes []uint32 // type index per code section function
}

// newWasmCtx builds a wasmCtx for the entry, or nil if the entry is
// not a WebAssembly module. syms must be sorted by address and not yet
// filtered: the text symbols correspond exactly, in order, to the code
// section functions.
func newWasmCtx(e *objfile.Entry, syms []objfile.Sym) *wasmCtx {
	wi := e.WasmInfo()
	if wi == nil {
		return nil
	}
	ctx := &wasmCtx{
		imports:   wi.Imports,
		types:     wi.Types,
		funcTypes: wi.FuncTypes,
	}
	for _, s := range syms {
		if s.Code == 'T' {
			ctx.funcNames = append(ctx.funcNames, s.Name)
			ctx.funcAddrs = append(ctx.funcAddrs, s.Addr)
			ctx.funcSizes = append(ctx.funcSizes, uint64(s.Size))
		}
	}
	return ctx
}

// funcName returns the symbolized name of the function with the given
// wasm function index (imports included), or "" if unknown.
func (ctx *wasmCtx) funcName(idx uint64) string {
	if idx < uint64(len(ctx.imports)) {
		return ctx.imports[idx]
	}
	if i := idx - uint64(len(ctx.imports)); i < uint64(len(ctx.funcNames)) {
		return ctx.funcNames[i]
	}
	return ""
}

// typeSig returns the rendered signature for a type index, or "".
func (ctx *wasmCtx) typeSig(idx uint64) string {
	if idx < uint64(len(ctx.types)) {
		return ctx.types[idx]
	}
	return ""
}

// funcSig returns the rendered signature of the function whose body
// starts at the given file offset, or "".
func (ctx *wasmCtx) funcSig(addr uint64) string {
	for i, a := range ctx.funcAddrs {
		if a == addr && i < len(ctx.funcTypes) {
			return ctx.typeSig(uint64(ctx.funcTypes[i]))
		}
	}
	return ""
}

// wasmGlobals is the fixed global layout emitted by
// cmd/link/internal/wasm (writeGlobalSec).
var wasmGlobals = []string{"SP", "CTXT", "g", "RET0", "RET1", "RET2", "RET3", "PAUSE"}

// decodeWasm disassembles the byte range [start, end), which normally
// is exactly one function body, invoking f for the local declarations
// and then each instruction.
func (d *Disasm) decodeWasm(start, end uint64, f func(pc, size uint64, file string, line int, text string)) {
	code := d.text[start-d.textStart : end-d.textStart]
	dec := &wasmDecoder{code: code, ctx: d.wasm}
	_, base := d.lookup(start)
	fullBody := false
	if base == start {
		// Decoding from the top of a function: the body begins with
		// its vector of local declarations.
		dec.atStart = true
		dec.sig = d.wasm.funcSig(start)
		for i, a := range d.wasm.funcAddrs {
			if a == start && start+d.wasm.funcSizes[i] == end {
				fullBody = true
			}
		}
	}
	for dec.off < len(dec.code) {
		off := dec.off
		text := dec.next()
		file, line, _ := d.pcln.PCToLine(start + uint64(off))
		f(start+uint64(off), uint64(dec.off-off), file, line, text)
	}
	if fullBody && (dec.depth != 0 || dec.lastOp != 0x0b) {
		// The body should consist of balanced blocks terminated by a
		// single end. Anything else means the decode drifted.
		file, line, _ := d.pcln.PCToLine(start)
		f(end, 0, file, line, fmt.Sprintf("?decode-error: body did not decode cleanly (depth=%d)", dec.depth))
	}
}

// A wasmDecoder decodes the instructions of one function body (or an
// arbitrary byte range) sequentially, tracking block nesting for
// indentation.
type wasmDecoder struct {
	code      []byte
	off       int
	depth     int
	atStart   bool   // next item is the local declaration vector
	sig       string // rendered signature of the function, if known
	ctx       *wasmCtx
	lastOp    byte
	truncated bool // current instruction ran off the end of the range
}

func (dec *wasmDecoder) byte() byte {
	if dec.off >= len(dec.code) {
		dec.truncated = true
		return 0
	}
	b := dec.code[dec.off]
	dec.off++
	return b
}

func (dec *wasmDecoder) uleb() uint64 {
	var v uint64
	var shift uint
	for {
		b := dec.byte()
		if dec.truncated || shift > 63 {
			dec.truncated = true
			dec.off = len(dec.code)
			return 0
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v
		}
		shift += 7
	}
}

func (dec *wasmDecoder) sleb() int64 {
	var v int64
	var shift uint
	for {
		b := dec.byte()
		if dec.truncated || shift > 63 {
			dec.truncated = true
			dec.off = len(dec.code)
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
	}
}

// next decodes the next item and returns its rendered text. It always
// makes progress: dec.off strictly increases.
func (dec *wasmDecoder) next() string {
	if dec.atStart {
		dec.atStart = false
		return dec.finish(0, dec.locals())
	}
	op := dec.byte()
	ent := wasmOps[op]
	if ent.name == "" {
		return dec.finish(op, fmt.Sprintf("?0x%02x", op))
	}
	text := ent.name
	switch ent.imm {
	case wimmNone:
	case wimmBlock:
		text += dec.blockType()
	case wimmLabel:
		text += fmt.Sprintf(" %d", dec.uleb())
	case wimmBrTable:
		text += dec.brTable()
	case wimmCall:
		idx := dec.uleb()
		if name := dec.ctx.funcName(idx); name != "" {
			text += " " + name
		} else {
			text += fmt.Sprintf(" %d", idx)
		}
	case wimmCallInd:
		typ := dec.uleb()
		table := dec.uleb()
		text += fmt.Sprintf(" type[%d]", typ)
		if table != 0 {
			text += fmt.Sprintf(" table=%d", table)
		}
		if sig := dec.ctx.typeSig(typ); sig != "" {
			text += " // " + sig
		}
	case wimmLocal:
		text += fmt.Sprintf(" %d", dec.uleb())
	case wimmGlobal:
		idx := dec.uleb()
		text += fmt.Sprintf(" %d", idx)
		if idx < uint64(len(wasmGlobals)) {
			text += " // " + wasmGlobals[idx]
		}
	case wimmMem:
		text += dec.memArg(uint64(ent.nalign))
	case wimmMemByte:
		dec.byte() // memory index, always 0
	case wimmI32, wimmI64:
		text += fmt.Sprintf(" %d", dec.sleb())
	case wimmF32:
		var bits uint32
		for i := 0; i < 4; i++ {
			bits |= uint32(dec.byte()) << (8 * i)
		}
		text += fmt.Sprintf(" %g", math.Float32frombits(bits))
	case wimmF64:
		var bits uint64
		for i := 0; i < 8; i++ {
			bits |= uint64(dec.byte()) << (8 * i)
		}
		text += fmt.Sprintf(" %g", math.Float64frombits(bits))
	case wimmSelectT:
		n := dec.uleb()
		for i := uint64(0); i < n && !dec.truncated; i++ {
			text += " " + wasmType(dec.byte())
		}
	case wimmRefNull:
		text += " " + wasmType(dec.byte())
	case wimmFuncRef, wimmTable:
		text += fmt.Sprintf(" %d", dec.uleb())
	case wimmPrefixFC:
		return dec.finish(op, dec.miscOp())
	case wimmPrefixFD:
		return dec.finish(op, dec.simdOp())
	case wimmPrefixFE:
		return dec.finish(op, dec.atomicOp())
	}
	return dec.finish(op, text)
}

// finish records the opcode, applies truncation markers and block
// indentation, and updates the nesting depth.
func (dec *wasmDecoder) finish(op byte, text string) string {
	dec.lastOp = op
	if dec.truncated {
		dec.truncated = false
		text += " ?(truncated)"
	}
	depth := dec.depth
	switch op {
	case 0x02, 0x03, 0x04: // block, loop, if
		dec.depth++
	case 0x05: // else prints at the depth of its if
		depth--
	case 0x0b: // end
		if dec.depth > 0 {
			dec.depth--
		}
		depth = dec.depth
	}
	if depth < 0 {
		depth = 0
	}
	return strings.Repeat("  ", depth) + text
}

// locals decodes the local declaration vector at the top of a body.
func (dec *wasmDecoder) locals() string {
	var parts []string
	n := dec.uleb()
	for i := uint64(0); i < n && !dec.truncated; i++ {
		count := dec.uleb()
		typ := dec.byte()
		parts = append(parts, fmt.Sprintf("%d x %s", count, wasmType(typ)))
	}
	text := "locals [" + strings.Join(parts, ", ") + "]"
	if dec.sig != "" {
		text += " // func type " + dec.sig
	}
	return text
}

func (dec *wasmDecoder) blockType() string {
	if dec.off >= len(dec.code) {
		dec.truncated = true
		return ""
	}
	switch b := dec.code[dec.off]; {
	case b == 0x40: // empty block type
		dec.off++
		return ""
	case b >= 0x6f && b <= 0x7f: // single value type
		dec.off++
		return " " + wasmType(b)
	default: // type index as sleb33
		idx := dec.sleb()
		text := fmt.Sprintf(" type[%d]", idx)
		if idx >= 0 {
			if sig := dec.ctx.typeSig(uint64(idx)); sig != "" {
				text += " // " + sig
			}
		}
		return text
	}
}

// brTable renders a br_table target vector, eliding very long ones
// (the entry dispatch of a Go function can have hundreds of targets).
func (dec *wasmDecoder) brTable() string {
	const maxShow = 16
	n := dec.uleb() // number of targets, excluding the default
	var b strings.Builder
	b.WriteString(" {")
	for i := uint64(0); i < n && !dec.truncated; i++ {
		t := dec.uleb()
		if i < maxShow {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%d", t)
		} else if i == maxShow {
			fmt.Fprintf(&b, ", ... (+%d more)", n-maxShow)
		}
	}
	fmt.Fprintf(&b, "} %d", dec.uleb()) // default target
	return b.String()
}

// memArg renders a memory immediate (alignment and offset). The
// alignment is omitted when it has the natural value for the access.
func (dec *wasmDecoder) memArg(nalign uint64) string {
	align := dec.uleb()
	offset := dec.uleb()
	text := ""
	if offset != 0 {
		text += fmt.Sprintf(" offset=%d", offset)
	}
	if align != nalign {
		text += fmt.Sprintf(" align=%d", align)
	}
	return text
}

// miscOp decodes a 0xFC-prefixed instruction (saturating truncations
// and bulk memory/table operations).
func (dec *wasmDecoder) miscOp() string {
	sub := dec.uleb()
	if name, ok := wasmMiscOps[sub]; ok {
		switch sub {
		case 8: // memory.init dataidx, memidx
			name += fmt.Sprintf(" %d", dec.uleb())
			dec.byte()
		case 9, 13: // data.drop, elem.drop
			name += fmt.Sprintf(" %d", dec.uleb())
		case 10: // memory.copy memidx, memidx
			dec.byte()
			dec.byte()
		case 11: // memory.fill memidx
			dec.byte()
		case 12, 14: // table.init, table.copy
			name += fmt.Sprintf(" %d %d", dec.uleb(), dec.uleb())
		case 15, 16, 17: // table.grow, table.size, table.fill
			name += fmt.Sprintf(" %d", dec.uleb())
		}
		return name
	}
	return fmt.Sprintf("?0xfc 0x%02x", sub)
}

// simdOp decodes a 0xFD-prefixed (SIMD) instruction. The Go compiler
// does not emit these; they are decoded generically, consuming the
// correct immediates so that the rest of the body stays in sync.
func (dec *wasmDecoder) simdOp() string {
	sub := dec.uleb()
	text := fmt.Sprintf("simd[0x%02x]", sub)
	switch {
	case sub <= 11, sub == 92, sub == 93: // loads/stores with memarg
		text += dec.memArg(0)
	case sub == 12, sub == 13: // v128.const, i8x16.shuffle: 16 bytes
		text += " 0x"
		for i := 0; i < 16; i++ {
			text = fmt.Sprintf("%s%02x", text, dec.byte())
		}
	case sub >= 21 && sub <= 34: // extract/replace lane
		text += fmt.Sprintf(" lane=%d", dec.byte())
	case sub >= 84 && sub <= 91: // load/store lane with memarg
		text += dec.memArg(0)
		text += fmt.Sprintf(" lane=%d", dec.byte())
	}
	return text
}

// atomicOp decodes a 0xFE-prefixed (threads) instruction.
func (dec *wasmDecoder) atomicOp() string {
	sub := dec.uleb()
	text := fmt.Sprintf("atomic[0x%02x]", sub)
	if sub == 3 { // atomic.fence
		dec.byte()
		return text
	}
	return text + dec.memArg(0)
}

func wasmType(t byte) string {
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

// Immediate encodings of wasm instructions.
type wasmImm uint8

const (
	wimmNone     wasmImm = iota
	wimmBlock            // block type
	wimmLabel            // label index
	wimmBrTable          // vector of label indices plus default
	wimmCall             // function index
	wimmCallInd          // type index, table index
	wimmLocal            // local index
	wimmGlobal           // global index
	wimmMem              // memarg: alignment, offset
	wimmMemByte          // single reserved byte (memory index)
	wimmI32              // signed leb128 (32 bit)
	wimmI64              // signed leb128 (64 bit)
	wimmF32              // 4 bytes, little endian
	wimmF64              // 8 bytes, little endian
	wimmSelectT          // vector of value types
	wimmRefNull          // reference type
	wimmFuncRef          // function index (ref.func)
	wimmTable            // table index
	wimmPrefixFC         // miscellaneous prefix
	wimmPrefixFD         // SIMD prefix
	wimmPrefixFE         // atomics prefix
)

type wasmOp struct {
	name   string
	imm    wasmImm
	nalign uint8 // log2 of the natural alignment, for wimmMem
}

// wasmOps maps opcode bytes to names and immediate encodings, per the
// WebAssembly spec (binary format), cross-checked against the encoder
// in cmd/internal/obj/wasm.
var wasmOps = [256]wasmOp{
	0x00: {name: "unreachable"},
	0x01: {name: "nop"},
	0x02: {name: "block", imm: wimmBlock},
	0x03: {name: "loop", imm: wimmBlock},
	0x04: {name: "if", imm: wimmBlock},
	0x05: {name: "else"},
	0x0b: {name: "end"},
	0x0c: {name: "br", imm: wimmLabel},
	0x0d: {name: "br_if", imm: wimmLabel},
	0x0e: {name: "br_table", imm: wimmBrTable},
	0x0f: {name: "return"},
	0x10: {name: "call", imm: wimmCall},
	0x11: {name: "call_indirect", imm: wimmCallInd},
	0x12: {name: "return_call", imm: wimmCall},
	0x13: {name: "return_call_indirect", imm: wimmCallInd},
	0x1a: {name: "drop"},
	0x1b: {name: "select"},
	0x1c: {name: "select", imm: wimmSelectT},
	0x20: {name: "local.get", imm: wimmLocal},
	0x21: {name: "local.set", imm: wimmLocal},
	0x22: {name: "local.tee", imm: wimmLocal},
	0x23: {name: "global.get", imm: wimmGlobal},
	0x24: {name: "global.set", imm: wimmGlobal},
	0x25: {name: "table.get", imm: wimmTable},
	0x26: {name: "table.set", imm: wimmTable},
	0x28: {name: "i32.load", imm: wimmMem, nalign: 2},
	0x29: {name: "i64.load", imm: wimmMem, nalign: 3},
	0x2a: {name: "f32.load", imm: wimmMem, nalign: 2},
	0x2b: {name: "f64.load", imm: wimmMem, nalign: 3},
	0x2c: {name: "i32.load8_s", imm: wimmMem, nalign: 0},
	0x2d: {name: "i32.load8_u", imm: wimmMem, nalign: 0},
	0x2e: {name: "i32.load16_s", imm: wimmMem, nalign: 1},
	0x2f: {name: "i32.load16_u", imm: wimmMem, nalign: 1},
	0x30: {name: "i64.load8_s", imm: wimmMem, nalign: 0},
	0x31: {name: "i64.load8_u", imm: wimmMem, nalign: 0},
	0x32: {name: "i64.load16_s", imm: wimmMem, nalign: 1},
	0x33: {name: "i64.load16_u", imm: wimmMem, nalign: 1},
	0x34: {name: "i64.load32_s", imm: wimmMem, nalign: 2},
	0x35: {name: "i64.load32_u", imm: wimmMem, nalign: 2},
	0x36: {name: "i32.store", imm: wimmMem, nalign: 2},
	0x37: {name: "i64.store", imm: wimmMem, nalign: 3},
	0x38: {name: "f32.store", imm: wimmMem, nalign: 2},
	0x39: {name: "f64.store", imm: wimmMem, nalign: 3},
	0x3a: {name: "i32.store8", imm: wimmMem, nalign: 0},
	0x3b: {name: "i32.store16", imm: wimmMem, nalign: 1},
	0x3c: {name: "i64.store8", imm: wimmMem, nalign: 0},
	0x3d: {name: "i64.store16", imm: wimmMem, nalign: 1},
	0x3e: {name: "i64.store32", imm: wimmMem, nalign: 2},
	0x3f: {name: "memory.size", imm: wimmMemByte},
	0x40: {name: "memory.grow", imm: wimmMemByte},
	0x41: {name: "i32.const", imm: wimmI32},
	0x42: {name: "i64.const", imm: wimmI64},
	0x43: {name: "f32.const", imm: wimmF32},
	0x44: {name: "f64.const", imm: wimmF64},
	0x45: {name: "i32.eqz"},
	0x46: {name: "i32.eq"},
	0x47: {name: "i32.ne"},
	0x48: {name: "i32.lt_s"},
	0x49: {name: "i32.lt_u"},
	0x4a: {name: "i32.gt_s"},
	0x4b: {name: "i32.gt_u"},
	0x4c: {name: "i32.le_s"},
	0x4d: {name: "i32.le_u"},
	0x4e: {name: "i32.ge_s"},
	0x4f: {name: "i32.ge_u"},
	0x50: {name: "i64.eqz"},
	0x51: {name: "i64.eq"},
	0x52: {name: "i64.ne"},
	0x53: {name: "i64.lt_s"},
	0x54: {name: "i64.lt_u"},
	0x55: {name: "i64.gt_s"},
	0x56: {name: "i64.gt_u"},
	0x57: {name: "i64.le_s"},
	0x58: {name: "i64.le_u"},
	0x59: {name: "i64.ge_s"},
	0x5a: {name: "i64.ge_u"},
	0x5b: {name: "f32.eq"},
	0x5c: {name: "f32.ne"},
	0x5d: {name: "f32.lt"},
	0x5e: {name: "f32.gt"},
	0x5f: {name: "f32.le"},
	0x60: {name: "f32.ge"},
	0x61: {name: "f64.eq"},
	0x62: {name: "f64.ne"},
	0x63: {name: "f64.lt"},
	0x64: {name: "f64.gt"},
	0x65: {name: "f64.le"},
	0x66: {name: "f64.ge"},
	0x67: {name: "i32.clz"},
	0x68: {name: "i32.ctz"},
	0x69: {name: "i32.popcnt"},
	0x6a: {name: "i32.add"},
	0x6b: {name: "i32.sub"},
	0x6c: {name: "i32.mul"},
	0x6d: {name: "i32.div_s"},
	0x6e: {name: "i32.div_u"},
	0x6f: {name: "i32.rem_s"},
	0x70: {name: "i32.rem_u"},
	0x71: {name: "i32.and"},
	0x72: {name: "i32.or"},
	0x73: {name: "i32.xor"},
	0x74: {name: "i32.shl"},
	0x75: {name: "i32.shr_s"},
	0x76: {name: "i32.shr_u"},
	0x77: {name: "i32.rotl"},
	0x78: {name: "i32.rotr"},
	0x79: {name: "i64.clz"},
	0x7a: {name: "i64.ctz"},
	0x7b: {name: "i64.popcnt"},
	0x7c: {name: "i64.add"},
	0x7d: {name: "i64.sub"},
	0x7e: {name: "i64.mul"},
	0x7f: {name: "i64.div_s"},
	0x80: {name: "i64.div_u"},
	0x81: {name: "i64.rem_s"},
	0x82: {name: "i64.rem_u"},
	0x83: {name: "i64.and"},
	0x84: {name: "i64.or"},
	0x85: {name: "i64.xor"},
	0x86: {name: "i64.shl"},
	0x87: {name: "i64.shr_s"},
	0x88: {name: "i64.shr_u"},
	0x89: {name: "i64.rotl"},
	0x8a: {name: "i64.rotr"},
	0x8b: {name: "f32.abs"},
	0x8c: {name: "f32.neg"},
	0x8d: {name: "f32.ceil"},
	0x8e: {name: "f32.floor"},
	0x8f: {name: "f32.trunc"},
	0x90: {name: "f32.nearest"},
	0x91: {name: "f32.sqrt"},
	0x92: {name: "f32.add"},
	0x93: {name: "f32.sub"},
	0x94: {name: "f32.mul"},
	0x95: {name: "f32.div"},
	0x96: {name: "f32.min"},
	0x97: {name: "f32.max"},
	0x98: {name: "f32.copysign"},
	0x99: {name: "f64.abs"},
	0x9a: {name: "f64.neg"},
	0x9b: {name: "f64.ceil"},
	0x9c: {name: "f64.floor"},
	0x9d: {name: "f64.trunc"},
	0x9e: {name: "f64.nearest"},
	0x9f: {name: "f64.sqrt"},
	0xa0: {name: "f64.add"},
	0xa1: {name: "f64.sub"},
	0xa2: {name: "f64.mul"},
	0xa3: {name: "f64.div"},
	0xa4: {name: "f64.min"},
	0xa5: {name: "f64.max"},
	0xa6: {name: "f64.copysign"},
	0xa7: {name: "i32.wrap_i64"},
	0xa8: {name: "i32.trunc_f32_s"},
	0xa9: {name: "i32.trunc_f32_u"},
	0xaa: {name: "i32.trunc_f64_s"},
	0xab: {name: "i32.trunc_f64_u"},
	0xac: {name: "i64.extend_i32_s"},
	0xad: {name: "i64.extend_i32_u"},
	0xae: {name: "i64.trunc_f32_s"},
	0xaf: {name: "i64.trunc_f32_u"},
	0xb0: {name: "i64.trunc_f64_s"},
	0xb1: {name: "i64.trunc_f64_u"},
	0xb2: {name: "f32.convert_i32_s"},
	0xb3: {name: "f32.convert_i32_u"},
	0xb4: {name: "f32.convert_i64_s"},
	0xb5: {name: "f32.convert_i64_u"},
	0xb6: {name: "f32.demote_f64"},
	0xb7: {name: "f64.convert_i32_s"},
	0xb8: {name: "f64.convert_i32_u"},
	0xb9: {name: "f64.convert_i64_s"},
	0xba: {name: "f64.convert_i64_u"},
	0xbb: {name: "f64.promote_f32"},
	0xbc: {name: "i32.reinterpret_f32"},
	0xbd: {name: "i64.reinterpret_f64"},
	0xbe: {name: "f32.reinterpret_i32"},
	0xbf: {name: "f64.reinterpret_i64"},
	0xc0: {name: "i32.extend8_s"},
	0xc1: {name: "i32.extend16_s"},
	0xc2: {name: "i64.extend8_s"},
	0xc3: {name: "i64.extend16_s"},
	0xc4: {name: "i64.extend32_s"},
	0xd0: {name: "ref.null", imm: wimmRefNull},
	0xd1: {name: "ref.is_null"},
	0xd2: {name: "ref.func", imm: wimmFuncRef},
	0xfc: {name: "misc", imm: wimmPrefixFC},
	0xfd: {name: "simd", imm: wimmPrefixFD},
	0xfe: {name: "atomic", imm: wimmPrefixFE},
}

// wasmMiscOps names the 0xFC-prefixed instructions.
var wasmMiscOps = map[uint64]string{
	0:  "i32.trunc_sat_f32_s",
	1:  "i32.trunc_sat_f32_u",
	2:  "i32.trunc_sat_f64_s",
	3:  "i32.trunc_sat_f64_u",
	4:  "i64.trunc_sat_f32_s",
	5:  "i64.trunc_sat_f32_u",
	6:  "i64.trunc_sat_f64_s",
	7:  "i64.trunc_sat_f64_u",
	8:  "memory.init",
	9:  "data.drop",
	10: "memory.copy",
	11: "memory.fill",
	12: "table.init",
	13: "elem.drop",
	14: "table.copy",
	15: "table.grow",
	16: "table.size",
	17: "table.fill",
}
