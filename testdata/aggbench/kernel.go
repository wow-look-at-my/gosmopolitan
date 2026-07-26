package main

// kernel.go -- a faithful Go port of web/js/grid-math.js aggregateInto +
// aggregateCell + writeCellTexel, so the JS and Go/wasm costs are measured on
// exactly the same work over exactly the same data.

const (
	stPresent = 0x01
	stSwapped = 0x02
	stChanged = 0x04

	classEmpty    = 0
	classReserved = 1
	classSwapped  = 7

	flagBoundary      = 1
	flagNewSinceMark  = 2
	flagChangedInCell = 4
)

// kindClass maps kind index -> cell class (CLASS_ANON..CLASS_GPU).
var kindClass = [5]byte{2, 3, 4, 5, 6}

// VMATable is the wire VMA table in struct-of-arrays form.
type VMATable struct {
	PageOff []int32
	Pages   []int32
	Kind    []uint8
}

// vmaStartsWithin reports whether any VMA's first page lies in [start, end).
func (t *VMATable) startsWithin(start, end int) bool {
	lo, hi := 0, len(t.PageOff)
	for lo < hi {
		mid := (lo + hi) / 2
		if int(t.PageOff[mid]) < start {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < len(t.PageOff) && int(t.PageOff[lo]) < end
}

// findVMA binary-searches for the VMA containing pageIndex, or -1.
func (t *VMATable) findVMA(pageIndex int) int {
	if len(t.PageOff) == 0 || pageIndex < 0 {
		return -1
	}
	lo, hi := 0, len(t.PageOff)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if int(t.PageOff[mid]) <= pageIndex {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if int(t.PageOff[lo]) <= pageIndex && pageIndex < int(t.PageOff[lo])+int(t.Pages[lo]) {
		return lo
	}
	return -1
}

// RefAggregateInto is the naive per-page, per-cell aggregation: the shape the
// JS had before it was rewritten to skip runs. It is the reference the fast
// path in fastagg.go is pinned against, and is not used by the benchmark.
func RefAggregateInto(cellBuf, states []byte, t *VMATable, basePage, rangePages, cells, pagesPerCell int, markStates []byte) {
	for c := 0; c < cells; c++ {
		start := basePage + c*pagesPerCell
		end := start + pagesPerCell
		if lim := basePage + rangePages; end > lim {
			end = lim
		}
		aggregateCellInto(cellBuf, c, states, t, start, end, markStates)
	}
	for i := cells * 4; i < len(cellBuf); i++ {
		cellBuf[i] = 0
	}
}

func aggregateCellInto(cellBuf []byte, cellIndex int, states []byte, t *VMATable, start, end int, markStates []byte) {
	pages := end - start
	if pages < 0 {
		pages = 0
	}
	presentCount, swappedCount, newPages := 0, 0, 0
	anyChanged := false
	var busyKind, allKind [5]int

	vi := t.findVMA(start)
	vEnd := 1 << 62
	vKind := 0
	if vi >= 0 {
		vEnd = int(t.PageOff[vi]) + int(t.Pages[vi])
		vKind = int(t.Kind[vi])
	}
	for p := start; p < end; p++ {
		for p >= vEnd {
			vi++
			if vi >= len(t.PageOff) {
				vEnd = 1 << 62
				vKind = 0
				break
			}
			vEnd = int(t.PageOff[vi]) + int(t.Pages[vi])
			vKind = int(t.Kind[vi])
		}
		s := states[p]
		allKind[vKind]++
		if s&stPresent != 0 {
			presentCount++
		}
		if s&stSwapped != 0 {
			swappedCount++
		}
		if s&stChanged != 0 {
			anyChanged = true
		}
		if s&(stPresent|stSwapped) != 0 {
			busyKind[vKind]++
		}
		if markStates != nil && s&stPresent != 0 && markStates[p]&stPresent == 0 {
			newPages++
		}
	}
	counts := &allKind
	if presentCount+swappedCount > 0 {
		counts = &busyKind
	}
	di := 0
	for i := 1; i < len(counts); i++ {
		if counts[i] > counts[di] {
			di = i
		}
	}

	// writeCellTexel
	o := cellIndex * 4
	var class byte
	switch {
	case pages == 0:
		class = classEmpty
	case swappedCount > 0:
		class = classSwapped
	case presentCount > 0:
		class = kindClass[di]
	default:
		class = classReserved
	}
	cellBuf[o] = class
	cellBuf[o+1] = 0
	committed := presentCount + swappedCount
	if pages == 0 {
		cellBuf[o+2] = 0
	} else {
		cellBuf[o+2] = byte((255*committed + pages/2) / pages)
	}
	var flags byte
	if t.startsWithin(start, end) {
		flags |= flagBoundary
	}
	if newPages > 0 {
		flags |= flagNewSinceMark
	}
	if anyChanged {
		flags |= flagChangedInCell
	}
	cellBuf[o+3] = flags
}

// DecodeKeyframePages expands a keyframe pages blob into the state array.
func DecodeKeyframePages(buf []byte, totalPages int) []byte {
	states := make([]byte, totalPages)
	pos, page := 0, 0
	for pos < len(buf) {
		runLen, n := uvarint(buf, pos)
		pos = n
		st := buf[pos]
		pos++
		for i := 0; i < runLen; i++ {
			states[page+i] = st
		}
		page += runLen
	}
	return states
}

func uvarint(buf []byte, pos int) (int, int) {
	x, mult := 0, 1
	for {
		b := buf[pos]
		pos++
		x += int(b&0x7f) * mult
		if b&0x80 == 0 {
			return x, pos
		}
		mult *= 128
	}
}
