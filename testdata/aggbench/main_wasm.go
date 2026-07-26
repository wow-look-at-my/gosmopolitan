//go:build js && wasm

package main

import "syscall/js"

// The wasm build owns its input: it generates the frame in Go so the module is
// self-contained and every toolchain under test sees byte-identical data. JS
// only starts passes and reads the clock, as a browser client would.
//
// bench_checksum exists so a run is not merely fast but RIGHT: a codegen change
// that alters the produced cell buffer shows up as a different checksum, and
// bench.js refuses to report a time for it.

var (
	gStates  []byte
	gMark    []byte
	gCellBuf []byte
	gEncoded []byte
	gTable   *VMATable
	gTotal   int
	gCells   int
	gPPC     int
)

// bench_setup(cells, pagesPerCell, withMark) builds the frame and the buffers.
func benchSetup(this js.Value, args []js.Value) any {
	gCells = args[0].Int()
	gPPC = args[1].Int()
	gStates, gTable, gTotal = SyntheticFrame(DefaultShape, 0x9E3779B97F4A7C15)
	gCellBuf = make([]byte, gCells*4+4096)
	gEncoded = EncodeKeyframePages(gStates)
	if args[2].Truthy() {
		// A mark snapshot with fewer present pages, so the new-since-mark
		// tally has real work to do.
		gMark, _, _ = SyntheticFrame(FrameShape{
			TotalPages: DefaultShape.TotalPages, VMAs: DefaultShape.VMAs,
			BusyPct: 2, AvgGap: DefaultShape.AvgGap * 2, AvgRun: DefaultShape.AvgRun,
		}, 0xD1B54A32D192ED03)
	} else {
		gMark = nil
	}
	return gTotal
}

// bench_aggregate() runs one full aggregation pass over the resident frame.
func benchAggregate(this js.Value, args []js.Value) any {
	AggregateInto(gCellBuf, gStates, gTable, 0, gTotal, gCells, gPPC, gMark)
	return nil
}

// bench_decode() runs the RLE keyframe decode, the other per-frame cost.
func benchDecode(this js.Value, args []js.Value) any {
	return len(DecodeKeyframePages(gEncoded, gTotal))
}

// bench_checksum() is an order-sensitive hash of the produced cell buffer.
func benchChecksum(this js.Value, args []js.Value) any {
	var h uint32 = 2166136261
	for _, b := range gCellBuf[:gCells*4] {
		h = (h ^ uint32(b)) * 16777619
	}
	return int(h & 0x7FFFFFFF)
}

func main() {
	js.Global().Set("bench_setup", js.FuncOf(benchSetup))
	js.Global().Set("bench_aggregate", js.FuncOf(benchAggregate))
	js.Global().Set("bench_decode", js.FuncOf(benchDecode))
	js.Global().Set("bench_checksum", js.FuncOf(benchChecksum))
	js.Global().Get("console").Call("log", "aggbench ready")
	select {} // keep the runtime alive for the exported calls
}
