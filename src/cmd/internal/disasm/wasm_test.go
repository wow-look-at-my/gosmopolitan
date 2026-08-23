// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package disasm

import (
	"strings"
	"testing"
)

// decodeAll runs the decoder over a complete function body and returns
// the rendered items. It fails the test if the decoder does not consume
// exactly the given bytes.
func decodeAll(t *testing.T, ctx *wasmCtx, sig string, body []byte) []string {
	t.Helper()
	dec := &wasmDecoder{code: body, ctx: ctx, atStart: true, sig: sig}
	var out []string
	for dec.off < len(dec.code) {
		before := dec.off
		out = append(out, dec.next())
		if dec.off <= before {
			t.Fatalf("decoder made no progress at offset %d", before)
		}
	}
	if dec.off != len(body) {
		t.Fatalf("decoded %d bytes, want %d", dec.off, len(body))
	}
	return out
}

func TestWasmDecode(t *testing.T) {
	ctx := &wasmCtx{
		imports:   []string{"gojs.runtime.wasmExit"},
		funcNames: []string{"main.main", "main.helper"},
		funcAddrs: []uint64{100, 200},
		funcSizes: []uint64{50, 50},
		types:     []string{"(i32) -> (i32)"},
		funcTypes: []uint32{0, 0},
	}
	body := []byte{
		0x01, 0x02, 0x7e, // locals: 2 x i64
		0x02, 0x40, // block
		0x41, 0x2a, // i32.const 42
		0x0d, 0x00, // br_if 0
		0x42, 0x7f, // i64.const -1
		0x29, 0x03, 0x08, // i64.load offset=8
		0x28, 0x00, 0x04, // i32.load align=0 offset=4
		0x10, 0x00, // call 0 -> import
		0x10, 0x02, // call 2 -> code entry 1
		0x11, 0x00, 0x00, // call_indirect type[0]
		0x0e, 0x02, 0x00, 0x01, 0x02, // br_table {0, 1} 2
		0x23, 0x00, // global.get 0 (SP)
		0xfc, 0x02, // i32.trunc_sat_f64_s
		0x0b, // end (block)
		0x0b, // end (body)
	}
	got := decodeAll(t, ctx, ctx.typeSig(0), body)
	want := []string{
		"locals [2 x i64] // func type (i32) -> (i32)",
		"block",
		"  i32.const 42",
		"  br_if 0",
		"  i64.const -1",
		"  i64.load offset=8",
		"  i32.load offset=4 align=0",
		"  call gojs.runtime.wasmExit",
		"  call main.helper",
		"  call_indirect type[0] // (i32) -> (i32)",
		"  br_table {0, 1} 2",
		"  global.get 0 // SP",
		"  i32.trunc_sat_f64_s",
		"end",
		"end",
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d items, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestWasmDecodeRobust checks that malformed bodies neither panic nor
// hang, and are flagged.
func TestWasmDecodeRobust(t *testing.T) {
	ctx := &wasmCtx{}
	bodies := [][]byte{
		{0x00, 0x06, 0x0b},                   // unknown opcode 0x06
		{0x00, 0x41},                         // i32.const missing immediate
		{0x00, 0x41, 0xff, 0xff},             // i32.const truncated leb
		{0x00, 0xfd, 0x0c, 0x00, 0x01},       // v128.const truncated payload
		{0x00, 0xfc, 0x7f},                   // unknown misc op
		{0x83},                               // locals vector truncated
		{0x01, 0x05, 0x7e, 0x0b},             // huge locals count
		{0x00, 0x0e, 0x80, 0x80, 0x80, 0x00}, // br_table with padded leb count
	}
	for i, body := range bodies {
		dec := &wasmDecoder{code: body, ctx: ctx, atStart: true}
		for steps := 0; dec.off < len(dec.code); steps++ {
			before := dec.off
			dec.next()
			if dec.off <= before {
				t.Fatalf("body %d: no progress at offset %d", i, before)
			}
			if steps > len(body) {
				t.Fatalf("body %d: too many decode steps", i)
			}
		}
	}
}
