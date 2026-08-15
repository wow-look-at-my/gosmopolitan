package main

// frame.go -- the input the kernel runs on.
//
// The benchmark generates its frame rather than shipping a capture: a real one
// is a megabyte of binary and would date immediately. SyntheticFrame builds to
// the statistics that actually drive the kernel's cost, taken from a captured
// 16 GiB target -- 4.53M pages, 3.4% of them non-reserved, ~263k runs averaging
// 17 pages, over 32 VMAs -- and lands near them (~3% busy in ~163k runs), which
// is what matters: the scan stops about as often, over the same span. It is
// seeded, so every run and every toolchain sees byte-identical input.
//
// ParseKeyframe is kept so a real capture can be dropped in when one is at
// hand (the memory-visualizer wire format, "MV" v4 binary keyframe).

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

type wireVMA struct {
	Start   string `json:"start"`
	PageOff int32  `json:"pageOff"`
	Pages   int32  `json:"pages"`
	Kind    string `json:"kind"`
}

type keyframeMeta struct {
	VMAs []wireVMA `json:"vmas"`
}

var kinds = []string{"anon", "heap", "stack", "file", "gpu"}

func kindIndex(k string) uint8 {
	for i, s := range kinds {
		if s == k {
			return uint8(i)
		}
	}
	return 0
}

// FrameShape is the statistical description SyntheticFrame builds to.
type FrameShape struct {
	TotalPages int // pages in the address space
	VMAs       int // regions the space is divided into
	BusyPct    int // percent of pages that are not reserved
	AvgGap     int // mean run of reserved pages between interesting ones
	AvgRun     int // mean run of interesting pages
}

// DefaultShape is drawn from a captured 16 GiB target: 4.53M pages, a few
// percent busy in short runs separated by ~33-page reserved gaps, 32 VMAs.
var DefaultShape = FrameShape{TotalPages: 4533214, VMAs: 32, BusyPct: 3, AvgGap: 33, AvgRun: 2}

// SyntheticFrame builds a page-state array and VMA table with the given shape.
// The VMA table is CONCATENATED - region i+1 begins exactly where region i
// ends and every page belongs to one - which is what the sampler puts on the
// wire and what both aggregation paths are written against.
func SyntheticFrame(shape FrameShape, seed uint64) ([]byte, *VMATable, int) {
	total := shape.TotalPages
	states := make([]byte, total)

	// xorshift64*, so the frame does not depend on math/rand's stream staying
	// stable across Go releases.
	rnd := seed
	if rnd == 0 {
		rnd = 1
	}
	next := func() uint64 {
		rnd ^= rnd >> 12
		rnd ^= rnd << 25
		rnd ^= rnd >> 27
		return rnd * 2685821657736338717
	}
	within := func(n int) int {
		if n <= 1 {
			return 0
		}
		return int(next() % uint64(n))
	}

	for p := 0; p < total; {
		// A reserved gap, then a short run of interesting pages.
		p += 1 + within(2*shape.AvgGap)
		if p >= total {
			break
		}
		run := 1 + within(2*shape.AvgRun)
		// PRESENT dominates; SWAPPED and CHANGED are the rarer cases, and a
		// page may carry more than one, exactly as the sampler emits them.
		s := byte(stPresent)
		switch v := within(100); {
		case v < 4:
			s = stSwapped
		case v < 9:
			s = stPresent | stChanged
		case v < 11:
			s = stPresent | stSwapped
		}
		for i := 0; i < run && p < total; i, p = i+1, p+1 {
			states[p] = s
		}
	}
	// Thin the busy pages down to the target density: the loop above overshoots
	// for small gaps, and the density is what sets how often the scan stops.
	busy := 0
	for _, s := range states {
		if s != 0 {
			busy++
		}
	}
	want := total * shape.BusyPct / 100
	for p := 0; p < total && busy > want; p++ {
		if states[p] != 0 && within(4) != 0 {
			states[p] = 0
			busy--
		}
	}

	t := &VMATable{}
	per := total / shape.VMAs
	for i := 0; i < shape.VMAs; i++ {
		off := i * per
		n := per
		if i == shape.VMAs-1 {
			n = total - off
		}
		t.PageOff = append(t.PageOff, int32(off))
		t.Pages = append(t.Pages, int32(n))
		t.Kind = append(t.Kind, uint8(within(len(kinds))))
	}
	return states, t, total
}

// EncodeKeyframePages is the inverse of DecodeKeyframePages: [runLen][state]
// records with LEB128 run lengths. It exists so the decode can be measured on
// the generated frame without a capture to read it from.
func EncodeKeyframePages(states []byte) []byte {
	out := make([]byte, 0, len(states)/8)
	for p := 0; p < len(states); {
		q := p + 1
		for q < len(states) && states[q] == states[p] {
			q++
		}
		n := uint64(q - p)
		for {
			b := byte(n & 0x7f)
			n >>= 7
			if n != 0 {
				b |= 0x80
			}
			out = append(out, b)
			if n == 0 {
				break
			}
		}
		out = append(out, states[p])
		p = q
	}
	return out
}

// ParseKeyframe decodes a v4 binary keyframe into states + the VMA table.
func ParseKeyframe(b []byte) (states []byte, t *VMATable, totalPages int, err error) {
	if len(b) < 36 || b[0] != 'M' || b[1] != 'V' || b[2] != 1 || b[3] != 1 {
		return nil, nil, 0, fmt.Errorf("not a v4 binary keyframe")
	}
	totalPages = int(binary.LittleEndian.Uint32(b[20:]))
	metaLen := int(binary.LittleEndian.Uint32(b[24:]))
	pagesLen := int(binary.LittleEndian.Uint32(b[28:]))
	off := 36
	var meta keyframeMeta
	if err := json.Unmarshal(b[off:off+metaLen], &meta); err != nil {
		return nil, nil, 0, err
	}
	states = DecodeKeyframePages(b[off+metaLen:off+metaLen+pagesLen], totalPages)
	t = &VMATable{}
	for _, v := range meta.VMAs {
		t.PageOff = append(t.PageOff, v.PageOff)
		t.Pages = append(t.Pages, v.Pages)
		t.Kind = append(t.Kind, kindIndex(v.Kind))
	}
	return states, t, totalPages, nil
}
