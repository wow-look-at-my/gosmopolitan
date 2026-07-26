package main

import (
	"bytes"
	"testing"
)

// The generated frame is the benchmark's input, so its shape has to be the one
// the kernel was tuned against - and it has to be identical every run and on
// every toolchain, or a measurement means nothing.
func TestSyntheticFrameShape(t *testing.T) {
	shape := FrameShape{TotalPages: 400000, VMAs: 8, BusyPct: 3, AvgGap: 33, AvgRun: 2}
	states, tbl, total := SyntheticFrame(shape, 0x9E3779B97F4A7C15)

	if total != shape.TotalPages || len(states) != shape.TotalPages {
		t.Fatalf("total = %d, len(states) = %d, want %d", total, len(states), shape.TotalPages)
	}
	if len(tbl.PageOff) != shape.VMAs {
		t.Fatalf("got %d VMAs, want %d", len(tbl.PageOff), shape.VMAs)
	}

	// The table must tile the space with no gaps and no overlap: both
	// aggregation paths are written against that and nothing else.
	if tbl.PageOff[0] != 0 {
		t.Errorf("first VMA starts at page %d, want 0", tbl.PageOff[0])
	}
	for i := 1; i < len(tbl.PageOff); i++ {
		if want := tbl.PageOff[i-1] + tbl.Pages[i-1]; tbl.PageOff[i] != want {
			t.Errorf("VMA %d starts at %d, want %d (gap or overlap)", i, tbl.PageOff[i], want)
		}
	}
	last := len(tbl.PageOff) - 1
	if end := int(tbl.PageOff[last] + tbl.Pages[last]); end != total {
		t.Errorf("table covers %d pages, want %d", end, total)
	}

	busy := 0
	for _, s := range states {
		if s != 0 {
			busy++
		}
	}
	if pct := 100 * float64(busy) / float64(total); pct < 1 || pct > 10 {
		t.Errorf("frame is %.1f%% busy, want roughly %d%%", pct, shape.BusyPct)
	}

	// Deterministic: same seed, same bytes; different seed, different bytes.
	if again, _, _ := SyntheticFrame(shape, 0x9E3779B97F4A7C15); !bytes.Equal(states, again) {
		t.Error("same seed produced a different frame")
	}
	if other, _, _ := SyntheticFrame(shape, 12345); bytes.Equal(states, other) {
		t.Error("different seeds produced the same frame")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	states, _, total := SyntheticFrame(
		FrameShape{TotalPages: 50000, VMAs: 4, BusyPct: 3, AvgGap: 33, AvgRun: 2}, 7)
	if got := DecodeKeyframePages(EncodeKeyframePages(states), total); !bytes.Equal(got, states) {
		t.Error("round trip changed the frame")
	}

	// Runs longer than a single LEB128 byte have to survive too.
	long := bytes.Repeat([]byte{stPresent}, 5000)
	if got := DecodeKeyframePages(EncodeKeyframePages(long), len(long)); !bytes.Equal(got, long) {
		t.Error("round trip lost a multi-byte run length")
	}
}
