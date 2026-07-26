package main

import (
	"testing"
)

func synth() ([]byte, *VMATable, int) {
	total := 40000
	states := make([]byte, total)
	t := &VMATable{
		PageOff: []int32{0, 10000, 25000},
		Pages:   []int32{10000, 15000, 15000},
		Kind:    []uint8{0, 1, 3},
	}
	for i := 0; i < total; i++ {
		switch {
		case i%97 == 0:
			states[i] = stPresent
		case i%1013 == 0:
			states[i] = stSwapped
		case i%501 == 0:
			states[i] = stPresent | stChanged
		}
	}
	return states, t, total
}

func TestAggregateInto(t *testing.T) {
	states, tbl, total := synth()
	cells, ppc := 2500, 16
	buf := make([]byte, cells*4+64)
	AggregateInto(buf, states, tbl, 0, total, cells, ppc, nil)
	nonEmpty := 0
	for c := 0; c < cells; c++ {
		if buf[c*4] != classEmpty {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		t.Fatal("every cell came out empty")
	}

	mark := make([]byte, total)
	AggregateInto(buf, states, tbl, 0, total, cells, ppc, mark)
	newFlag := false
	for c := 0; c < cells; c++ {
		if buf[c*4+3]&flagNewSinceMark != 0 {
			newFlag = true
		}
	}
	if !newFlag {
		t.Fatal("no cell was flagged new-since-mark")
	}

}

func TestFindVMAAndStartsWithin(t *testing.T) {
	_, tbl, _ := synth()
	for _, c := range []struct{ page, want int }{{0, 0}, {24999, 1}, {39999, 2}, {-1, -1}} {
		if got := tbl.findVMA(c.page); got != c.want {
			t.Errorf("findVMA(%d) = %d, want %d", c.page, got, c.want)
		}
	}

	if !tbl.startsWithin(9999, 10001) {
		t.Error("startsWithin missed the VMA starting at 10000")
	}
	if tbl.startsWithin(11000, 12000) {
		t.Error("startsWithin found a VMA start where there is none")
	}

}

func TestDecodeKeyframePagesRoundTrip(t *testing.T) {
	// [runLen][state] records: 3 pages of 1, 2 pages of 0, 1 page of 5.
	blob := []byte{3, stPresent, 2, 0, 1, stPresent | stChanged}
	got := DecodeKeyframePages(blob, 6)
	want := []byte{1, 1, 1, 0, 0, 5}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("page %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestUvarintMultiByte(t *testing.T) {
	v, n := uvarint([]byte{0xAC, 0x02}, 0)
	if v != 300 || n != 2 {
		t.Errorf("uvarint = (%d, %d), want (300, 2)", v, n)
	}

}

func TestKindIndex(t *testing.T) {
	for _, c := range []struct {
		kind string
		want uint8
	}{{"heap", 1}, {"gpu", 4}, {"nope", 0}} {
		if got := kindIndex(c.kind); got != c.want {
			t.Errorf("kindIndex(%q) = %d, want %d", c.kind, got, c.want)
		}
	}

}
