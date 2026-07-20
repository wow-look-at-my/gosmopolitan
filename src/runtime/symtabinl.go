// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"unsafe" // also for linkname
)

// inlinedCall is the decoded form of one entry in the FUNCDATA_InlTree
// table. In the table, entries are packed inlinedCallSize-byte records
// with no padding: funcID at offset 0, then nameOff, parentPc and
// startLine as unaligned target-endian 32-bit fields at offsets 1, 5
// and 9. The unwinder decodes records with unaligned loads (see
// inlineUnwinder.call), so the packed layout is safe on every GOARCH.
type inlinedCall struct {
	funcID    abi.FuncID // type of the called function
	nameOff   int32      // offset into pclntab for name of called function
	parentPc  int32      // position of an instruction whose source position is the call site (offset from entry)
	startLine int32      // line number of start of function (func keyword/TEXT directive)
}

// inlinedCallSize is the encoded size in bytes of one FUNCDATA_InlTree
// entry. The unwinder indexes the table randomly by PCDATA_InlTreeIndex
// value, so entries have a fixed stride.
//
// Keep in sync with cmd/link/internal/ld/pcln.go:genInlTreeSym.
const inlinedCallSize = 13

// Byte offsets of the inlinedCall fields within an encoded record.
// Keep in sync with inlinedCall, call and genInlTreeSym.
const (
	inlinedCallFuncID    = 0 // uint8
	inlinedCallNameOff   = 1 // unaligned uint32
	inlinedCallParentPc  = 5 // unaligned uint32
	inlinedCallStartLine = 9 // unaligned uint32
)

// An inlineUnwinder iterates over the stack of inlined calls at a PC by
// decoding the inline table. The last step of iteration is always the frame of
// the physical function, so there's always at least one frame.
//
// This is typically used as:
//
//	for u, uf := newInlineUnwinder(...); uf.valid(); uf = u.next(uf) { ... }
//
// Implementation note: This is used in contexts that disallow write barriers.
// Hence, the constructor returns this by value and pointer receiver methods
// must not mutate pointer fields. Also, we keep the mutable state in a separate
// struct mostly to keep both structs SSA-able, which generates much better
// code.
type inlineUnwinder struct {
	f       funcInfo
	inlTree unsafe.Pointer // base of this function's FUNCDATA_InlTree table, or nil
	entry   uintptr        // f.entry(), cached; set iff inlTree != nil
}

// An inlineFrame is a position in an inlineUnwinder.
type inlineFrame struct {
	// pc is the PC giving the file/line metadata of the current frame. This is
	// always a "call PC" (not a "return PC"). This is 0 when the iterator is
	// exhausted.
	pc uintptr

	// index is the index of the current record in inlTree, or -1 if we are in
	// the outermost function.
	index int32
}

// newInlineUnwinder creates an inlineUnwinder initially set to the inner-most
// inlined frame at PC. PC should be a "call PC" (not a "return PC").
//
// This unwinder uses non-strict handling of PC because it's assumed this is
// only ever used for symbolic debugging. If things go really wrong, it'll just
// fall back to the outermost frame.
//
// newInlineUnwinder should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/phuslu/log
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname newInlineUnwinder
func newInlineUnwinder(f funcInfo, pc uintptr) (inlineUnwinder, inlineFrame) {
	inldata := funcdata(f, abi.FUNCDATA_InlTree)
	if inldata == nil {
		return inlineUnwinder{f: f}, inlineFrame{pc: pc, index: -1}
	}
	// Cache f.entry(): next needs it for every inlined frame, and the
	// textAddr call behind it is too big to inline.
	u := inlineUnwinder{f: f, inlTree: inldata, entry: f.entry()}
	return u, u.resolveInternal(pc)
}

// record returns a pointer to the index'th encoded entry of the inline
// tree. The hot paths (next, srcFunc) load the fields they need from it
// directly at the inlinedCall* offsets: decoding all four fields, as call
// does, costs too much for those methods to stay inlinable.
func (u *inlineUnwinder) record(index int32) unsafe.Pointer {
	return add(u.inlTree, uintptr(index)*inlinedCallSize)
}

// call decodes the index'th entry of the inline tree. It does not
// allocate and is safe on the system stack. It is the reference decoder
// for the record layout; hot paths read single fields from record
// instead.
func (u *inlineUnwinder) call(index int32) inlinedCall {
	p := u.record(index)
	return inlinedCall{
		funcID:    abi.FuncID(*(*uint8)(p)),
		nameOff:   int32(readUnaligned32(add(p, inlinedCallNameOff))),
		parentPc:  int32(readUnaligned32(add(p, inlinedCallParentPc))),
		startLine: int32(readUnaligned32(add(p, inlinedCallStartLine))),
	}
}

func (u *inlineUnwinder) resolveInternal(pc uintptr) inlineFrame {
	// Equivalent to pcdatavalue1(u.f, abi.PCDATA_InlTreeIndex, pc, false)
	// - pcvalue returns -1 for pcdataoff's absent-table 0 - but with the
	// constant-table presence test and offset lookup folded by inlining.
	// Conveniently, -1 on error is the same value we use for the
	// outermost frame.
	index, _ := pcvalue(u.f, pcdataoff(u.f, abi.PCDATA_InlTreeIndex), pc, false)
	return inlineFrame{pc: pc, index: index}
}

func (uf inlineFrame) valid() bool {
	return uf.pc != 0
}

// next returns the frame representing uf's logical caller.
func (u *inlineUnwinder) next(uf inlineFrame) inlineFrame {
	if uf.index < 0 {
		uf.pc = 0
		return uf
	}
	parentPc := int32(readUnaligned32(add(u.record(uf.index), inlinedCallParentPc)))
	return u.resolveInternal(u.entry + uintptr(parentPc))
}

// isInlined returns whether uf is an inlined frame.
func (u *inlineUnwinder) isInlined(uf inlineFrame) bool {
	return uf.index >= 0
}

// srcFunc returns the srcFunc representing the given frame.
//
// srcFunc should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/phuslu/log
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
// The go:linkname is below.
func (u *inlineUnwinder) srcFunc(uf inlineFrame) srcFunc {
	if uf.index < 0 {
		return u.f.srcFunc()
	}
	p := u.record(uf.index)
	return srcFunc{
		u.f.datap,
		int32(readUnaligned32(add(p, inlinedCallNameOff))),
		int32(readUnaligned32(add(p, inlinedCallStartLine))),
		abi.FuncID(*(*uint8)(p)),
	}
}

//go:linkname badSrcFunc runtime.(*inlineUnwinder).srcFunc
func badSrcFunc(*inlineUnwinder, inlineFrame) srcFunc

// srcFuncID returns just the funcID of the srcFunc representing uf.
// Unlike srcFunc it is cheap enough to inline, so the traceback loops
// that only elide wrappers use it instead.
func (u *inlineUnwinder) srcFuncID(uf inlineFrame) abi.FuncID {
	if uf.index < 0 {
		return u.f.funcID
	}
	return abi.FuncID(*(*uint8)(u.record(uf.index)))
}

// fileLine returns the file name and line number of the call within the given
// frame. As a convenience, for the innermost frame, it returns the file and
// line of the PC this unwinder was started at (often this is a call to another
// physical function).
//
// It returns "?", 0 if something goes wrong.
func (u *inlineUnwinder) fileLine(uf inlineFrame) (file string, line int) {
	file, line32 := funcline1(u.f, uf.pc, false)
	return file, int(line32)
}

// fileLinePieces is like fileLine, but returns the file name in two
// table-aliasing pieces (see funcfilePieces). It does not allocate.
func (u *inlineUnwinder) fileLinePieces(uf inlineFrame) (dir, file string, line int) {
	dir, file, line32 := funcline1Pieces(u.f, uf.pc, false)
	return dir, file, int(line32)
}
