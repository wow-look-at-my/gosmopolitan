package main

import (
	"math/rand"
	"testing"
)

// The fast aggregation is an optimization, so a byte of divergence from the
// naive per-page reference is a bug by definition. Same discipline as the JS
// side's refAggregateCell test and internal/sampler's parallel-scan parity
// test: randomized inputs over BOTH shapes that occur in practice - a sparse
// target that is almost all reserved, and a dense one that is almost all
// present - since the two exercise opposite halves of the run skipping.

type spaceShape struct {
	name string
	// fill writes a page-state array whose statistics match the shape.
	fill func(rnd *rand.Rand, states []byte)
}

var spaceShapes = []spaceShape{
	{"sparse", func(rnd *rand.Rand, states []byte) {
		// ~97% reserved in long runs, like a game reserving terabytes.
		for p := 0; p < len(states); {
			run := 1 + rnd.Intn(400)
			var s byte
			if rnd.Intn(100) < 3 {
				s = randState(rnd)
			}
			for i := 0; i < run && p < len(states); i, p = i+1, p+1 {
				states[p] = s
			}
		}
	}},
	{"dense", func(rnd *rand.Rand, states []byte) {
		// ~80% present in long uniform runs, like a leaking process.
		for p := 0; p < len(states); {
			run := 1 + rnd.Intn(600)
			s := byte(stPresent)
			switch {
			case rnd.Intn(100) < 12:
				s = 0
			case rnd.Intn(100) < 8:
				s = randState(rnd)
			}
			for i := 0; i < run && p < len(states); i, p = i+1, p+1 {
				states[p] = s
			}
		}
	}},
	{"shredded", func(rnd *rand.Rand, states []byte) {
		// No runs at all: every page independent. Defeats every fast path,
		// so it checks the scalar tails rather than the word loops.
		for p := range states {
			if rnd.Intn(3) != 0 {
				states[p] = randState(rnd)
			}
		}
	}},
}

func randState(rnd *rand.Rand) byte {
	return byte(rnd.Intn(8)) // every combination of PRESENT/SWAPPED/CHANGED
}

// randomTable builds a pageOff-sorted VMA table that tiles [0, total) with no
// gaps, which is the precondition both aggregators are written against and
// what the wire actually carries: the sampler hands out a CONCATENATED layout,
// so VMA i+1 starts exactly where VMA i ends and every page belongs to one.
// (The two paths deliberately disagree about a page in a hole - the reference
// adopts the next VMA's kind, the fast path treats it as kind 0 - so feeding
// them holes would be testing an input neither is defined for.)
func randomTable(rnd *rand.Rand, total int) *VMATable {
	t := &VMATable{}
	for p := 0; p < total; {
		n := 1 + rnd.Intn(3000)
		if p+n > total {
			n = total - p
		}
		t.PageOff = append(t.PageOff, int32(p))
		t.Pages = append(t.Pages, int32(n))
		t.Kind = append(t.Kind, uint8(rnd.Intn(5)))
		p += n
	}
	return t
}

func TestFastAggregateMatchesReference(t *testing.T) {
	for _, shape := range spaceShapes {
		t.Run(shape.name, func(t *testing.T) {
			for iter := 0; iter < 40; iter++ {
				rnd := rand.New(rand.NewSource(int64(iter) * 7919))
				total := 1 + rnd.Intn(9000)
				states := make([]byte, total)
				shape.fill(rnd, states)
				tbl := randomTable(rnd, total)

				var mark []byte
				if iter%2 == 0 {
					mark = make([]byte, total)
					shape.fill(rnd, mark)
				}

				// Half the iterations use a layout the WORD path accepts (a
				// word-aligned base and a whole number of 8-page words per
				// cell) and half use one it must refuse, so both paths get
				// swept. Left to chance the word path would almost never run
				// here, and it is the one that runs in production.
				var basePage, ppc int
				if iter%2 == 0 {
					basePage = 8 * rnd.Intn(1+min(8, total/8))
					ppc = 8 * (1 + rnd.Intn(5))
				} else {
					basePage = rnd.Intn(min(64, total))
					ppc = 1 + rnd.Intn(40)
				}
				if basePage >= total {
					basePage = 0
				}
				rangePages := total - basePage
				cells := (rangePages + ppc - 1) / ppc

				got := make([]byte, cells*4+37)
				want := make([]byte, cells*4+37)
				for i := range got {
					got[i], want[i] = 0xAA, 0xAA // catch bytes neither path writes
				}
				AggregateInto(got, states, tbl, basePage, rangePages, cells, ppc, mark)
				RefAggregateInto(want, states, tbl, basePage, rangePages, cells, ppc, mark)

				for i := 0; i < cells*4; i++ {
					if got[i] != want[i] {
						t.Fatalf("iter %d: cell %d byte %d: fast=%d ref=%d"+
							" (total=%d base=%d ppc=%d cells=%d mark=%v)",
							iter, i/4, i%4, got[i], want[i],
							total, basePage, ppc, cells, mark != nil)
					}
				}
			}
		})
	}
}

// The word-at-a-time scanners have to agree with the obvious byte loops for
// every alignment of start and end, which is where an off-by-one in the
// head/tail handling would hide.
func TestScannersMatchByteLoops(t *testing.T) {
	rnd := rand.New(rand.NewSource(11))
	states := make([]byte, 300)
	for i := range states {
		if rnd.Intn(4) != 0 {
			states[i] = randState(rnd)
		}
	}
	for from := 0; from < 40; from++ {
		for _, end := range []int{from, from + 1, from + 7, from + 9, from + 33, 300, 400} {
			if end < from {
				continue
			}
			if got, want := nextNonZeroState(states, from, end), refNextNonZero(states, from, end); got != want {
				t.Errorf("nextNonZeroState(%d, %d) = %d, want %d", from, end, got, want)
			}
			if got, want := countPresentPages(states, from, end), refCountPresent(states, from, end); got != want {
				t.Errorf("countPresentPages(%d, %d) = %d, want %d", from, end, got, want)
			}
			if from < len(states) && end > from {
				if got, want := nextStateChange(states, from, end), refNextChange(states, from, end); got != want {
					t.Errorf("nextStateChange(%d, %d) = %d, want %d", from, end, got, want)
				}
			}
		}
	}
}

func refNextNonZero(states []byte, from, end int) int {
	lim := min(end, len(states))
	for p := max(from, 0); p < lim; p++ {
		if states[p] != 0 {
			return p
		}
	}
	return end
}

func refNextChange(states []byte, from, end int) int {
	lim := min(end, len(states))
	for p := from + 1; p < lim; p++ {
		if states[p] != states[from] {
			return p
		}
	}
	return lim
}

func refCountPresent(states []byte, from, to int) int {
	n := 0
	for p := from; p < min(to, len(states)); p++ {
		if states[p]&stPresent != 0 {
			n++
		}
	}
	return n
}
