package main

// fastagg.go -- the aggregation kernel, in two paths.
//
// kernel.go keeps the naive per-page version as RefAggregateInto; the
// randomized parity test in fastagg_test.go pins everything here against it.
//
// The WORD path is the one that runs on a real frame. A page's state is one
// byte and a cell is a fixed number of pages, so when a cell is a whole number
// of 8-page words on a word boundary, the entire cell is a couple of 64-bit
// loads: PRESENT is bit 0 of each byte lane, so the count of present pages in
// a word is one popcount, and the same for SWAPPED one bit over. There is no
// run detection, no per-page loop and no cell-transition bookkeeping at all -
// the cost is a small constant per cell, whatever the data looks like. An
// all-zero cell is exactly the reserved texel the prefill already wrote, so it
// costs one OR and a branch and nothing is stored.
//
// The RUN path handles the layouts the word path cannot take (a cell that is
// not a whole number of words, or does not start on one) by skipping runs:
// jump the reserved gaps eight pages per load, and take each uniform run whole.
// It is what the JS client does, and it is what the parity test exercises most,
// since the test sweeps pages-per-cell from 1 to 40.

import (
	"encoding/binary"
	"math/bits"
	"unsafe"
)

// presentBits has the PRESENT bit set in each of the eight byte lanes, so
// `w & presentBits` isolates one bit per page and its popcount is the number
// of present pages in that word.
const presentBits = 0x0101010101010101

// noVMA is the sentinel VMA end used past the end of the table.
const noVMA = 1 << 62

// nextNonZeroState returns the first page in [from, end) whose state byte is
// nonzero, or end when the range holds none. Pages past the end of the state
// array count as zero.
//
// There is deliberately no alignment prologue. The JS version needs one because
// a Uint32Array view has to start on a 4-byte boundary; wasm and amd64 both
// load an unaligned 8 bytes as happily as an aligned one, so the word loop
// starts wherever the caller left off. That matters more than it looks: on a
// real frame the gaps between interesting pages average ~33 pages, so a
// prologue per call would eat most of what the word scan saves. Locating the
// byte inside the word is one ctz, not a rescan.
func nextNonZeroState(states []byte, from, end int) int {
	p := from
	if p < 0 {
		p = 0
	}
	lim := end
	if lim > len(states) {
		lim = len(states)
	}
	for p+8 <= lim {
		if w := binary.LittleEndian.Uint64(states[p : p+8]); w != 0 {
			return p + bits.TrailingZeros64(w)/8
		}
		p += 8
	}
	for p < lim && states[p] == 0 {
		p++
	}
	if p < lim {
		return p
	}
	return end
}

// nextStateChange returns the first index in (from, end) whose state byte
// differs from states[from], or the limit the run reached. Every page of a
// uniform run contributes identically to its cell, so the run can be taken
// whole.
func nextStateChange(states []byte, from, end int) int {
	s := states[from]
	lim := end
	if lim > len(states) {
		lim = len(states)
	}
	p := from + 1
	splat := uint64(s) * presentBits // 0xSSSS..SS, exact for s < 256
	for p+8 <= lim {
		// XOR against the splatted byte: any lane that differs becomes
		// nonzero, and the lowest such lane is the change index.
		if w := binary.LittleEndian.Uint64(states[p:p+8]) ^ splat; w != 0 {
			return p + bits.TrailingZeros64(w)/8
		}
		p += 8
	}
	for p < lim && states[p] == s {
		p++
	}
	return p
}

// countPresentPages counts the pages in [from, to) whose PRESENT bit is set,
// eight at a time.
func countPresentPages(states []byte, from, to int) int {
	n := 0
	p := from
	lim := to
	if lim > len(states) {
		lim = len(states)
	}
	for p+8 <= lim {
		n += bits.OnesCount64(binary.LittleEndian.Uint64(states[p:p+8]) & presentBits)
		p += 8
	}
	for p < lim {
		if states[p]&stPresent != 0 {
			n++
		}
		p++
	}
	return n
}

// prefillReserved writes the fully-reserved texel (class RESERVED, no heat, no
// committed fraction, no flags) into every one of the first `cells` texels and
// zeroes whatever trails them. The doubling copy turns the fill into a handful
// of bulk copies rather than a per-byte loop. Both paths rely on this: a cell
// with nothing in it is left exactly as the prefill wrote it.
func prefillReserved(cellBuf []byte, cells int) {
	n := cells * 4
	if n > len(cellBuf) {
		n = len(cellBuf)
	}
	head := cellBuf[:n]
	if len(head) >= 4 {
		head[0], head[1], head[2], head[3] = classReserved, 0, 0, 0
		for i := 4; i < len(head); i *= 2 {
			copy(head[i:], head[:i])
		}
	}
	tail := cellBuf[n:]
	for i := range tail {
		tail[i] = 0
	}
}

// log2Exact returns log2(n) when n is a positive power of two, else -1. The
// layout picker always chooses a power-of-two pages-per-cell, which turns the
// committed-fraction division into a shift; nothing here depends on that being
// true, it just gets slower when it is not.
func log2Exact(n int) int {
	if n > 0 && n&(n-1) == 0 {
		return bits.TrailingZeros(uint(n))
	}
	return -1
}

// writeCellTexel packs one cell's facts into its RGBA texel. `shift` is
// log2(pages) when pages is a power of two, else -1; the committed fraction is
// the only division in the per-cell path and it would otherwise run once per
// busy cell, ~131k times on a real frame.
func writeCellTexel(cellBuf []byte, cellIndex, pages, present, swapped int, changed bool, newPages, kind, shift int) {
	var class byte
	switch {
	case pages <= 0:
		class = classEmpty
	case swapped > 0:
		class = classSwapped
	case present > 0:
		class = kindClass[kind]
	default:
		class = classReserved
	}
	var heat byte
	switch {
	case pages <= 0:
		heat = 0
	case shift >= 0:
		heat = byte((255*(present+swapped) + pages/2) >> uint(shift))
	default:
		heat = byte((255*(present+swapped) + pages/2) / pages)
	}
	var flags byte
	if newPages > 0 {
		flags |= flagNewSinceMark
	}
	if changed {
		flags |= flagChangedInCell
	}
	// One 32-bit store rather than four byte stores, each with its own bounds
	// check. PutUint32 names the byte order explicitly, so the texel layout
	// (class, 0, heat, flags) is the same on any host.
	o := cellIndex * 4
	binary.LittleEndian.PutUint32(cellBuf[o:o+4],
		uint32(class)|uint32(heat)<<16|uint32(flags)<<24)
}

// dominantKind resolves a cell's winning kind: the lazily-tracked single kind
// when only one turned up, otherwise the lowest-indexed maximum of the full
// tally (the same tie-break the naive reference uses).
func dominantKind(busyKind *[5]int, cellKind int, mixed bool) int {
	if !mixed {
		return cellKind
	}
	di := 0
	for i := 1; i < 5; i++ {
		if busyKind[i] > busyKind[di] {
			di = i
		}
	}
	return di
}

// AggregateInto re-aggregates the whole viewed range into the RGBA8 cell
// buffer. Byte-for-byte identical to RefAggregateInto, and to the JS
// aggregateInto, for a gap-free pageOff-sorted VMA table.
func AggregateInto(cellBuf, states []byte, t *VMATable, basePage, rangePages, cells, pagesPerCell int, markStates []byte) {
	viewEnd := basePage + rangePages
	prefillReserved(cellBuf, cells)

	// The word path needs each cell to be a whole number of 8-page words on a
	// word boundary, and needs the state array to actually cover the view.
	if pagesPerCell%8 == 0 && basePage%8 == 0 && viewEnd <= len(states) &&
		(markStates == nil || viewEnd <= len(markStates)) {
		aggregateByWord(cellBuf, states, t, basePage, viewEnd, cells, pagesPerCell, markStates)
	} else {
		aggregateByRun(cellBuf, states, t, basePage, viewEnd, pagesPerCell, markStates)
	}

	// A VMA start lands in exactly one cell; stamping last means it survives
	// both the reserved prefill and any cell finalization.
	for i := 0; i < len(t.PageOff); i++ {
		off := int(t.PageOff[i])
		if off >= basePage && off < viewEnd {
			cellBuf[((off-basePage)/pagesPerCell)*4+3] |= flagBoundary
		}
	}
}

// wordView reinterprets a byte slice as 64-bit words, or returns nil when the
// slice does not start on an 8-byte boundary. Go's allocator hands back
// 8-aligned memory for anything this size, so nil is a guard rather than an
// expected outcome, and the caller simply takes the byte path instead.
//
// This is worth the unsafe. Every access the word path makes is 8-aligned by
// construction, and indexing a []uint64 the compiler can see the length of
// costs no bounds check, where reading the same bytes through a slice
// expression costs one per load - measured at 1.20x of the whole pass, since
// there are ~550k of them.
func wordView(b []byte) []uint64 {
	if len(b) < 8 || uintptr(unsafe.Pointer(&b[0]))%8 != 0 {
		return nil
	}
	return unsafe.Slice((*uint64)(unsafe.Pointer(&b[0])), len(b)/8)
}

// aggregateByWord finds the work by scanning and then does it by counting
// bits. Neither half alone is enough: walking every cell wastes the 54% of
// cells that hold nothing, and walking every run pays per-run bookkeeping on
// the cells that do. So the scan jumps reserved space one word per load, and
// whatever cell a nonzero word falls in is finished in one shot - two loads
// and four popcounts for a 16-page cell - after which the scan resumes past
// that cell.
//
// It reads the state array as words throughout, and only ever needs to know
// WHICH WORD is nonzero, never which byte inside it - so nothing here depends
// on byte order, and the counting is order-free by nature.
//
// Preconditions, checked by the caller: pagesPerCell is a multiple of 8,
// basePage is word-aligned (so every cell starts on a word), and the state and
// mark arrays cover the view.
func aggregateByWord(cellBuf, states []byte, t *VMATable, basePage, viewEnd, cells, pagesPerCell int, markStates []byte) {
	sw := wordView(states)
	if sw == nil {
		aggregateByRun(cellBuf, states, t, basePage, viewEnd, pagesPerCell, markStates)
		return
	}
	var mw []uint64
	if markStates != nil {
		if mw = wordView(markStates); mw == nil {
			aggregateByRun(cellBuf, states, t, basePage, viewEnd, pagesPerCell, markStates)
			return
		}
	}

	nv := len(t.PageOff)
	ppcShift := log2Exact(pagesPerCell)
	wpc := pagesPerCell / 8 // words per cell
	wpcShift := log2Exact(wpc)

	// Cells [0, nFull) are whole; at most one more is cut short by the view and
	// is left to the reference path below.
	nFull := (viewEnd - basePage) / pagesPerCell
	if nFull > cells {
		nFull = cells
	}
	wStart := basePage / 8
	wEnd := wStart + nFull*wpc
	if wEnd > len(sw) {
		wEnd = len(sw)
	}

	si := 0        // forward VMA cursor: cells ascend, so it only moves right
	insideEnd := 0 // cells below this are known to lie wholly inside VMA si
	kind := 0
	wi := wStart
	for {
		// The skip loop is kept to itself so it stays three instructions and a
		// backedge: this is where all the reserved space goes by.
		for wi < wEnd && sw[wi] == 0 {
			wi++
		}
		if wi >= wEnd {
			break
		}
		// --- finish the cell this word is in, then resume past that cell ---
		var c int
		if wpcShift >= 0 {
			c = (wi - wStart) >> uint(wpcShift)
		} else {
			c = (wi - wStart) / wpc
		}
		cw0 := wStart + c*wpc
		cw1 := cw0 + wpc

		// The VMA only has to be looked up when the cell leaves the span the
		// last lookup proved: with a handful of VMAs over hundreds of
		// thousands of cells, that is a compare per cell and a lookup per VMA.
		if c >= insideEnd {
			cs := basePage + c*pagesPerCell
			ce := cs + pagesPerCell
			for si < nv && cs >= int(t.PageOff[si])+int(t.Pages[si]) {
				si++
			}
			// A cell straddling a VMA edge, or outside the table, takes the
			// reference path: at most one per VMA, and it keeps the tie-break
			// rules defined in exactly one place.
			if si >= nv || cs < int(t.PageOff[si]) || ce > int(t.PageOff[si])+int(t.Pages[si]) {
				aggregateCellInto(cellBuf, c, states, t, cs, ce, markStates)
				wi = cw1
				continue
			}
			insideEnd = (int(t.PageOff[si]) + int(t.Pages[si]) - basePage) / pagesPerCell
			kind = int(t.Kind[si])
		}

		// Ranging over the sub-slices is what makes this bounds-check free.
		cw := sw[cw0:cw1]
		var or uint64
		present, swapped := 0, 0
		for _, w := range cw {
			or |= w
			present += bits.OnesCount64(w & presentBits)
			swapped += bits.OnesCount64((w >> 1) & presentBits)
		}
		newPages := 0
		if mw != nil {
			m := mw[cw0:cw1]
			m = m[:len(cw)] // same length by construction; says so to the compiler
			for i, ws := range cw {
				// Present now and not present at mark time, one bit per page.
				newPages += bits.OnesCount64(ws &^ m[i] & presentBits)
			}
		}
		// The whole cell is inside VMA si, so every busy page shares its kind
		// and the five-way tally cannot change the answer.
		writeCellTexel(cellBuf, c, pagesPerCell, present, swapped,
			(or>>2)&presentBits != 0, newPages, kind, ppcShift)
		wi = cw1
	}

	// The one cell the view cuts short, plus any pages past the last whole word.
	if nFull < cells {
		cs := basePage + nFull*pagesPerCell
		if cs < viewEnd {
			ce := cs + pagesPerCell
			if ce > viewEnd {
				ce = viewEnd
			}
			aggregateCellInto(cellBuf, nFull, states, t, cs, ce, markStates)
		}
	}
}

// aggregateByRun is the general path: it visits only pages with a nonzero
// state byte, reached by jumping reserved runs eight pages per load, and takes
// each uniform run whole. Cost is O(interesting pages + cells).
func aggregateByRun(cellBuf, states []byte, t *VMATable, basePage, viewEnd, pagesPerCell int, markStates []byte) {
	nv := len(t.PageOff)
	ppcShift := log2Exact(pagesPerCell)

	// The kind tally is kept lazily. With a handful of VMAs over many cells,
	// virtually every cell lies inside ONE of them, so a cell almost never
	// needs a histogram: remember the first busy kind and its count, and only
	// fall back to the five-way tally when a second kind actually turns up.
	var busyKind [5]int
	cellKind, cellKindCount, mixedKinds := 0, 0, false
	curCell := -1
	present, swapped, newPages := 0, 0, 0
	changed := false
	si, vEnd, vKind := 0, -1, 0

	// The two scanners are spelled out inline rather than called: Go will not
	// inline a function containing a loop, so each call is a real wasm frame
	// with a stack check, and this pass makes hundreds of thousands of them.
	lim := viewEnd
	if lim > len(states) {
		lim = len(states)
	}
	p := basePage
	if p < 0 {
		p = 0
	}
	for {
		// --- nextNonZeroState, inlined ---
		hit := false
		for p+8 <= lim {
			if w := binary.LittleEndian.Uint64(states[p : p+8]); w != 0 {
				p += bits.TrailingZeros64(w) / 8
				hit = true
				break
			}
			p += 8
		}
		if !hit {
			for p < lim && states[p] == 0 {
				p++
			}
			if p >= lim {
				break
			}
		}

		var c int
		if ppcShift >= 0 {
			c = (p - basePage) >> uint(ppcShift)
		} else {
			c = (p - basePage) / pagesPerCell
		}
		if c != curCell {
			if curCell >= 0 {
				finalizeRunCell(cellBuf, curCell, basePage, viewEnd, pagesPerCell, ppcShift,
					present, swapped, changed, newPages, dominantKind(&busyKind, cellKind, mixedKinds))
			}
			curCell = c
			present, swapped, newPages, changed = 0, 0, 0, false
			cellKind, cellKindCount, mixedKinds = 0, 0, false
		}
		if p >= vEnd {
			for si < nv && p >= int(t.PageOff[si])+int(t.Pages[si]) {
				si++
			}
			switch {
			case si < nv && p >= int(t.PageOff[si]):
				vEnd = int(t.PageOff[si]) + int(t.Pages[si])
				vKind = int(t.Kind[si])
			case si < nv:
				vEnd = int(t.PageOff[si]) // in a hole before the next VMA
				vKind = 0
			default:
				vEnd = noVMA // past the table
				vKind = 0
			}
		}
		// Take the whole uniform run at once, clipped to this cell and this VMA.
		s := states[p]
		rlim := basePage + (c+1)*pagesPerCell
		if lim < rlim {
			rlim = lim
		}
		if vEnd < rlim {
			rlim = vEnd
		}
		// --- nextStateChange, inlined ---
		q := p + 1
		splat := uint64(s) * presentBits
		qhit := false
		for q+8 <= rlim {
			if w := binary.LittleEndian.Uint64(states[q:q+8]) ^ splat; w != 0 {
				q += bits.TrailingZeros64(w) / 8
				qhit = true
				break
			}
			q += 8
		}
		if !qhit {
			for q < rlim && states[q] == s {
				q++
			}
		}
		n := q - p

		if s&stPresent != 0 {
			present += n
		}
		if s&stSwapped != 0 {
			swapped += n
		}
		if s&stChanged != 0 {
			changed = true
		}
		if s&(stPresent|stSwapped) != 0 {
			switch {
			case mixedKinds:
				busyKind[vKind] += n
			case cellKindCount == 0:
				cellKind, cellKindCount = vKind, n
			case vKind == cellKind:
				cellKindCount += n
			default:
				// Second kind in this cell: start the real tally, seeded with
				// everything counted so far.
				busyKind = [5]int{}
				busyKind[cellKind] = cellKindCount
				busyKind[vKind] += n
				mixedKinds = true
			}
		}
		if markStates != nil && s&stPresent != 0 {
			newPages += n - countPresentPages(markStates, p, q)
		}
		p = q
	}
	if curCell >= 0 {
		finalizeRunCell(cellBuf, curCell, basePage, viewEnd, pagesPerCell, ppcShift,
			present, swapped, changed, newPages, dominantKind(&busyKind, cellKind, mixedKinds))
	}
}

// finalizeRunCell writes the texel for a cell the run path has finished with,
// working out the cell's page span (the last one may be cut short by the view).
func finalizeRunCell(cellBuf []byte, cell, basePage, viewEnd, pagesPerCell, ppcShift,
	present, swapped int, changed bool, newPages, kind int) {
	cs := basePage + cell*pagesPerCell
	ce := cs + pagesPerCell
	shift := ppcShift
	if ce > viewEnd {
		ce = viewEnd
		shift = -1 // a short final cell is not a clean power of two
	}
	writeCellTexel(cellBuf, cell, ce-cs, present, swapped, changed, newPages, kind, shift)
}
