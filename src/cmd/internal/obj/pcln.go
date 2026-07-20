// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package obj

import (
	"cmd/internal/goobj"
	"cmd/internal/objabi"
	"encoding/binary"
	"fmt"
	"log"
)

// funcpctab writes to dst a pc-value table mapping the code in func to the values
// returned by valfunc parameterized by arg. The invocation of valfunc to update the
// current value is, for each p,
//
//	sym = valfunc(func, p, 0, arg);
//	record sym.P as value at p->pc;
//	sym = valfunc(func, p, 1, arg);
//
// where func is the function, val is the current value, p is the instruction being
// considered, and arg can be used to further parameterize valfunc.
func funcpctab(ctxt *Link, func_ *LSym, desc string, valfunc func(*Link, *LSym, int32, *Prog, int32, any) int32, arg any) *LSym {
	dbg := desc == ctxt.Debugpcln
	dst := []byte{}
	sym := &LSym{
		Type:      objabi.SRODATA,
		Attribute: AttrContentAddressable | AttrPcdata,
	}

	if dbg {
		ctxt.Logf("funcpctab %s [valfunc=%s]\n", func_.Name, desc)
	}

	val := int32(-1)
	oldval := val
	fn := func_.Func()
	if fn.Text == nil {
		// Return the empty symbol we've built so far.
		return sym
	}

	pc := fn.Text.Pc

	if dbg {
		ctxt.Logf("%6x %6d %v\n", uint64(pc), val, fn.Text)
	}

	pendingDelta := int32(0)
	started := false
	for p := fn.Text; p != nil; p = p.Link {
		// Update val. If it's not changing, keep going.
		val = valfunc(ctxt, func_, val, p, 0, arg)

		if val == oldval && started {
			val = valfunc(ctxt, func_, val, p, 1, arg)
			if dbg {
				ctxt.Logf("%6x %6s %v\n", uint64(p.Pc), "", p)
			}
			continue
		}

		// If the pc of the next instruction is the same as the
		// pc of this instruction, this instruction is not a real
		// instruction. Keep going, so that we only emit a delta
		// for a true instruction boundary in the program.
		if p.Link != nil && p.Link.Pc == p.Pc {
			val = valfunc(ctxt, func_, val, p, 1, arg)
			if dbg {
				ctxt.Logf("%6x %6s %v\n", uint64(p.Pc), "", p)
			}
			continue
		}

		// The table is a sequence of (value, pc) pairs, where each
		// pair states that the given value is in effect from the current position
		// up to the given pc, which becomes the new current position.
		// To generate the table as we scan over the program instructions,
		// we emit a "(value" when pc == func->value, and then
		// each time we observe a change in value we emit ", pc) (value".
		// When the scan is over, we emit the closing ", pc)".
		//
		// The table is delta-encoded; appendPCPair describes the wire
		// format of each pair. Because a pair's pc delta only becomes
		// known at the NEXT value change (or at the end of the
		// function), the value delta is held in pendingDelta until
		// then and both halves are emitted together.

		if dbg {
			ctxt.Logf("%6x %6d %v\n", uint64(p.Pc), val, p)
		}

		if started {
			pcdelta := (p.Pc - pc) / int64(ctxt.Arch.MinLC)
			dst = appendPCPair(dst, pendingDelta, pcdelta)
			pc = p.Pc
		}

		pendingDelta = val - oldval
		oldval = val
		started = true
		val = valfunc(ctxt, func_, val, p, 1, arg)
	}

	if started {
		if dbg {
			ctxt.Logf("%6x done\n", uint64(fn.Text.Pc+func_.Size))
		}
		v := (func_.Size - pc) / int64(ctxt.Arch.MinLC)
		if v < 0 {
			ctxt.Diag("negative pc offset: %v", v)
		}
		dst = appendPCPair(dst, pendingDelta, v)
		// add terminating 0 byte in the value-delta position
		dst = append(dst, 0)
	}

	if dbg {
		ctxt.Logf("wrote %d bytes to %p\n", len(dst), dst)
		for _, p := range dst {
			ctxt.Logf(" %02x", p)
		}
		ctxt.Logf("\n")
	}

	sym.Size = int64(len(dst))
	sym.P = dst
	return sym
}

// appendPCPair appends one (value delta, pc delta) pair of a pc-value
// table to dst. The pc delta is in units of the architecture's minimum
// instruction size and, except for the final pair of a table, is at
// least 1.
//
// The pair grammar, chosen by the first byte b:
//
//   - b >= 0x80: a packed pair. The value delta is bits 4-6 minus 4
//     (-4..3) and the pc delta is bits 0-3 plus 1 (1..16), so
//     b = 0x80 | (delta+4)<<4 | (pcdelta-1).
//   - b == 0x7F: an escape. A zig-zag varint value delta whose first
//     byte would collide with the packed or escape space (any multi-byte
//     varint, or the single byte 0x7F) follows, then a uvarint pc delta.
//   - b <= 0x7E: b itself is a one-byte zig-zag value delta, followed by
//     a uvarint pc delta. The byte 0x00 in this position terminates the
//     table - value deltas of 0 only ever occur in a table's first pair
//     (relative to the implicit initial value of -1), where readers
//     accept it - so pairs after the first always have b != 0.
//
// Keep in sync with the decoders: PCIter.Next below,
// runtime.step (runtime/symtab.go) and debug/gosym.(*LineTable).step.
func appendPCPair(dst []byte, delta int32, pcdelta int64) []byte {
	if -4 <= delta && delta <= 3 && 1 <= pcdelta && pcdelta <= 16 {
		return append(dst, 0x80|byte(delta+4)<<4|byte(pcdelta-1))
	}
	var buf [binary.MaxVarintLen64]byte
	if u := uint64((int64(delta) << 1) ^ (int64(delta) >> 63)); u >= 0x7F {
		dst = append(dst, 0x7F)
		n := binary.PutUvarint(buf[:], u)
		dst = append(dst, buf[:n]...)
	} else {
		dst = append(dst, byte(u))
	}
	n := binary.PutUvarint(buf[:], uint64(pcdelta))
	return append(dst, buf[:n]...)
}

// pctofileline computes either the file number (arg == 0)
// or the line number (arg == 1) to use at p.
// Because p.Pos applies to p, phase == 0 (before p)
// takes care of the update.
func pctofileline(ctxt *Link, sym *LSym, oldval int32, p *Prog, phase int32, arg any) int32 {
	if p.As == ATEXT || p.As == ANOP || p.Pos.Line() == 0 || phase == 1 {
		return oldval
	}
	f, l := ctxt.getFileIndexAndLine(p.Pos)
	if arg == nil {
		return l
	}
	pcln := arg.(*Pcln)
	pcln.UsedFiles[goobj.CUFileIndex(f)] = struct{}{}
	return int32(f)
}

// pcinlineState holds the state used to create a function's inlining
// tree and the PC-value table that maps PCs to nodes in that tree.
type pcinlineState struct {
	globalToLocal map[int]int
	localTree     InlTree
}

// addBranch adds a branch from the global inlining tree in ctxt to
// the function's local inlining tree, returning the index in the local tree.
func (s *pcinlineState) addBranch(ctxt *Link, globalIndex int) int {
	if globalIndex < 0 {
		return -1
	}

	localIndex, ok := s.globalToLocal[globalIndex]
	if ok {
		return localIndex
	}

	// Since tracebacks don't include column information, we could
	// use one node for multiple calls of the same function on the
	// same line (e.g., f(x) + f(y)). For now, we use one node for
	// each inlined call.
	call := ctxt.InlTree.nodes[globalIndex]
	call.Parent = s.addBranch(ctxt, call.Parent)
	localIndex = len(s.localTree.nodes)
	s.localTree.nodes = append(s.localTree.nodes, call)
	s.globalToLocal[globalIndex] = localIndex
	return localIndex
}

func (s *pcinlineState) setParentPC(ctxt *Link, globalIndex int, pc int32) {
	localIndex, ok := s.globalToLocal[globalIndex]
	if !ok {
		// We know where to unwind to when we need to unwind a body identified
		// by globalIndex. But there may be no instructions generated by that
		// body (it's empty, or its instructions were CSEd with other things, etc.).
		// In that case, we don't need an unwind entry.
		// TODO: is this really right? Seems to happen a whole lot...
		return
	}
	s.localTree.setParentPC(localIndex, pc)
}

// pctoinline computes the index into the local inlining tree to use at p.
// If p is not the result of inlining, pctoinline returns -1. Because p.Pos
// applies to p, phase == 0 (before p) takes care of the update.
func (s *pcinlineState) pctoinline(ctxt *Link, sym *LSym, oldval int32, p *Prog, phase int32, arg any) int32 {
	if phase == 1 {
		return oldval
	}

	posBase := ctxt.PosTable.Pos(p.Pos).Base()
	if posBase == nil {
		return -1
	}

	globalIndex := posBase.InliningIndex()
	if globalIndex < 0 {
		return -1
	}

	if s.globalToLocal == nil {
		s.globalToLocal = make(map[int]int)
	}

	return int32(s.addBranch(ctxt, globalIndex))
}

// pctospadj computes the sp adjustment in effect.
// It is oldval plus any adjustment made by p itself.
// The adjustment by p takes effect only after p, so we
// apply the change during phase == 1.
func pctospadj(ctxt *Link, sym *LSym, oldval int32, p *Prog, phase int32, arg any) int32 {
	if oldval == -1 { // starting
		oldval = 0
	}
	if phase == 0 {
		return oldval
	}
	if oldval+p.Spadj < -10000 || oldval+p.Spadj > 1100000000 {
		ctxt.Diag("overflow in spadj: %d + %d = %d", oldval, p.Spadj, oldval+p.Spadj)
		ctxt.DiagFlush()
		log.Fatalf("bad code")
	}

	return oldval + p.Spadj
}

// pctopcdata computes the pcdata value in effect at p.
// A PCDATA instruction sets the value in effect at future
// non-PCDATA instructions.
// Since PCDATA instructions have no width in the final code,
// it does not matter which phase we use for the update.
func pctopcdata(ctxt *Link, sym *LSym, oldval int32, p *Prog, phase int32, arg any) int32 {
	if phase == 0 || p.As != APCDATA || p.From.Offset != int64(arg.(uint32)) {
		return oldval
	}
	if int64(int32(p.To.Offset)) != p.To.Offset {
		ctxt.Diag("overflow in PCDATA instruction: %v", p)
		ctxt.DiagFlush()
		log.Fatalf("bad code")
	}

	return int32(p.To.Offset)
}

func linkpcln(ctxt *Link, cursym *LSym) {
	pcln := &cursym.Func().Pcln
	pcln.UsedFiles = make(map[goobj.CUFileIndex]struct{})

	npcdata := 0
	nfuncdata := 0
	for p := cursym.Func().Text; p != nil; p = p.Link {
		// Find the highest ID of any used PCDATA table. This ignores PCDATA table
		// that consist entirely of "-1", since that's the assumed default value.
		//   From.Offset is table ID
		//   To.Offset is data
		if p.As == APCDATA && p.From.Offset >= int64(npcdata) && p.To.Offset != -1 { // ignore -1 as we start at -1, if we only see -1, nothing changed
			npcdata = int(p.From.Offset + 1)
		}
		// Find the highest ID of any FUNCDATA table.
		//   From.Offset is table ID
		if p.As == AFUNCDATA && p.From.Offset >= int64(nfuncdata) {
			nfuncdata = int(p.From.Offset + 1)
		}
	}

	pcln.Pcdata = make([]*LSym, npcdata)
	pcln.Funcdata = make([]*LSym, nfuncdata)

	pcln.Pcsp = funcpctab(ctxt, cursym, "pctospadj", pctospadj, nil)
	pcln.Pcfile = funcpctab(ctxt, cursym, "pctofile", pctofileline, pcln)
	pcln.Pcline = funcpctab(ctxt, cursym, "pctoline", pctofileline, nil)

	// Check that all the Progs used as inline markers are still reachable.
	// See issue #40473.
	fn := cursym.Func()
	inlMarkProgs := make(map[*Prog]struct{}, len(fn.InlMarks))
	for _, inlMark := range fn.InlMarks {
		inlMarkProgs[inlMark.p] = struct{}{}
	}
	for p := fn.Text; p != nil; p = p.Link {
		delete(inlMarkProgs, p)
	}
	if len(inlMarkProgs) > 0 {
		ctxt.Diag("one or more instructions used as inline markers are no longer reachable")
	}

	pcinlineState := new(pcinlineState)
	pcln.Pcinline = funcpctab(ctxt, cursym, "pctoinline", pcinlineState.pctoinline, nil)
	for _, inlMark := range fn.InlMarks {
		pcinlineState.setParentPC(ctxt, int(inlMark.id), int32(inlMark.p.Pc))
	}
	pcln.InlTree = pcinlineState.localTree
	if ctxt.Debugpcln == "pctoinline" && len(pcln.InlTree.nodes) > 0 {
		ctxt.Logf("-- inlining tree for %s:\n", cursym)
		dumpInlTree(ctxt, pcln.InlTree)
		ctxt.Logf("--\n")
	}

	// tabulate which pc and func data we have.
	havepc := make([]uint32, (npcdata+31)/32)
	havefunc := make([]uint32, (nfuncdata+31)/32)
	for p := fn.Text; p != nil; p = p.Link {
		if p.As == AFUNCDATA {
			if (havefunc[p.From.Offset/32]>>uint64(p.From.Offset%32))&1 != 0 {
				ctxt.Diag("multiple definitions for FUNCDATA $%d", p.From.Offset)
			}
			havefunc[p.From.Offset/32] |= 1 << uint64(p.From.Offset%32)
		}

		if p.As == APCDATA && p.To.Offset != -1 {
			havepc[p.From.Offset/32] |= 1 << uint64(p.From.Offset%32)
		}
	}

	// pcdata.
	for i := 0; i < npcdata; i++ {
		if (havepc[i/32]>>uint(i%32))&1 == 0 {
			// use an empty symbol.
			pcln.Pcdata[i] = &LSym{
				Type:      objabi.SRODATA,
				Attribute: AttrContentAddressable | AttrPcdata,
			}
		} else {
			pcln.Pcdata[i] = funcpctab(ctxt, cursym, "pctopcdata", pctopcdata, any(uint32(i)))
		}
	}

	// funcdata
	if nfuncdata > 0 {
		for p := fn.Text; p != nil; p = p.Link {
			if p.As != AFUNCDATA {
				continue
			}
			i := int(p.From.Offset)
			if p.To.Type != TYPE_MEM || p.To.Offset != 0 {
				panic(fmt.Sprintf("bad funcdata: %v", p))
			}
			pcln.Funcdata[i] = p.To.Sym
		}
	}
}

// PCIter iterates over encoded pcdata tables.
type PCIter struct {
	p       []byte
	PC      uint32
	NextPC  uint32
	PCScale uint32
	Value   int32
	start   bool
	Done    bool
}

// NewPCIter creates a PCIter with a scale factor for the PC step size.
func NewPCIter(pcScale uint32) *PCIter {
	it := new(PCIter)
	it.PCScale = pcScale
	return it
}

// Next advances it to the Next pc.
// The pair encoding is described at appendPCPair.
func (it *PCIter) Next() {
	it.PC = it.NextPC
	if it.Done {
		return
	}
	if len(it.p) == 0 {
		it.Done = true
		return
	}

	if b := it.p[0]; b >= 0x80 {
		// Packed pair.
		it.p = it.p[1:]
		it.start = false
		it.Value += int32(b>>4&7) - 4
		it.NextPC = it.PC + (uint32(b&0xF)+1)*it.PCScale
		return
	}

	// Value delta
	var val uint64
	if b := it.p[0]; b == 0x7F {
		// Escaped multi-byte value delta.
		it.p = it.p[1:]
		var n int
		val, n = binary.Uvarint(it.p)
		if n <= 0 {
			log.Fatalf("bad Value varint in pciterNext: read %v", n)
		}
		it.p = it.p[n:]
	} else {
		if b == 0 && !it.start {
			it.Done = true
			it.p = it.p[1:]
			return
		}
		val = uint64(b)
		it.p = it.p[1:]
	}

	it.start = false
	it.Value += int32(int64(val>>1) ^ -int64(val&1))

	// pc delta
	pc, n := binary.Uvarint(it.p)
	if n <= 0 {
		log.Fatalf("bad pc varint in pciterNext: read %v", n)
	}
	it.p = it.p[n:]

	it.NextPC = it.PC + uint32(pc)*it.PCScale
}

// init prepares it to iterate over p,
// and advances it to the first pc.
func (it *PCIter) Init(p []byte) {
	it.p = p
	it.PC = 0
	it.NextPC = 0
	it.Value = -1
	it.start = true
	it.Done = false
	it.Next()
}
