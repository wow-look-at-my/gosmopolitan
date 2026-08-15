package main

import "testing"

// Go benchmarks over the same generated frame the wasm build uses, so a change
// can be measured natively (fast to iterate on) before it is measured under
// node. BenchmarkScanOnly is the floor: walking the state array and finding
// every nonzero word, with no cell work at all.

const (
	benchCells = 283326
	benchPPC   = 16
)

var (
	benchStates []byte
	benchTbl    *VMATable
	benchTotal  int
	benchBuf    []byte
	benchMark   []byte
	benchBlob   []byte
)

func loadFrame() {
	if benchStates != nil {
		return
	}
	benchStates, benchTbl, benchTotal = SyntheticFrame(DefaultShape, 0x9E3779B97F4A7C15)
	benchBuf = make([]byte, benchCells*4+4096)
	benchBlob = EncodeKeyframePages(benchStates)
	benchMark, _, _ = SyntheticFrame(FrameShape{
		TotalPages: DefaultShape.TotalPages, VMAs: DefaultShape.VMAs,
		BusyPct: 2, AvgGap: DefaultShape.AvgGap * 2, AvgRun: DefaultShape.AvgRun,
	}, 0xD1B54A32D192ED03)
}

func BenchmarkAggregate(b *testing.B) {
	loadFrame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AggregateInto(benchBuf, benchStates, benchTbl, 0, benchTotal, benchCells, benchPPC, nil)
	}
}

func BenchmarkAggregateMark(b *testing.B) {
	loadFrame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AggregateInto(benchBuf, benchStates, benchTbl, 0, benchTotal, benchCells, benchPPC, benchMark)
	}
}

// BenchmarkAggregateRunPath measures the path taken when the layout is not
// word-friendly, by shifting the base page off a word boundary.
func BenchmarkAggregateRunPath(b *testing.B) {
	loadFrame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AggregateInto(benchBuf, benchStates, benchTbl, 1, benchTotal-1, benchCells, benchPPC, nil)
	}
}

func BenchmarkDecode(b *testing.B) {
	loadFrame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodeKeyframePages(benchBlob, benchTotal)
	}
}

func BenchmarkPrefill(b *testing.B) {
	loadFrame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prefillReserved(benchBuf, benchCells)
	}
}

func BenchmarkScanOnly(b *testing.B) {
	loadFrame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		for p := nextNonZeroState(benchStates, 0, benchTotal); p < benchTotal; {
			q := nextStateChange(benchStates, p, benchTotal)
			n++
			p = nextNonZeroState(benchStates, q, benchTotal)
		}
		if n == 0 {
			b.Fatal("no runs in the frame")
		}
	}
}
