// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

// framebench is a frame-loop allocation benchmark for js/wasm.
//
// The JS harness (bench.js) drives it via wasm exports: bench_frame
// allocates ~10k short-lived objects of mixed sizes per frame, keeping
// each frame's objects alive for a few frames so the heap churns like a
// real per-frame workload (scene graphs, particle systems, protocol
// decoding). bench_numgc reports the number of completed GC cycles so
// the harness can report how many ran during the measured window.
//
// Frame timing lives on the JS side: the harness measures the wall time
// of each bench_frame call, which is where GC assist work, GC
// start/termination pauses, and any in-frame background marking land.
package main

import "runtime"

// node is a small pointer-bearing object, the typical unit of per-frame
// allocation churn.
type node struct {
	id    int64
	frame int64
	next  *node
	data  []byte
}

const (
	smallPerFrame = 7000 // small pointer-bearing structs
	medPerFrame   = 2500 // 64..256 byte slices
	largePerFrame = 500  // 512..1536 byte slices
	// liveFrames is how many frames each frame's allocations stay
	// reachable, so the live heap churns instead of being garbage
	// immediately.
	liveFrames = 3
)

// graphNodes is the size of the persistent, pointer-rich live heap (a
// stand-in for a scene graph / entity system / DOM mirror). The GC has
// to re-mark all of it every cycle, which is what makes GC cycles
// expensive relative to a frame budget.
const graphNodes = 400000

type graphNode struct {
	left, right *graphNode
	payload     *node
	id          int64
}

var (
	ring    [liveFrames][]*node
	bigRing [liveFrames][][]byte
	frameNo int64
	// sink defeats any escape-analysis heroics.
	sink *node

	graph       []*graphNode
	graphMutIdx int
)

func buildGraph() {
	graph = make([]*graphNode, graphNodes)
	for i := range graph {
		graph[i] = &graphNode{id: int64(i)}
	}
	for i, g := range graph {
		g.left = graph[(i*7+1)%graphNodes]
		g.right = graph[(i*131+17)%graphNodes]
		if i%8 == 0 {
			g.payload = &node{id: int64(i)}
		}
	}
}

//go:wasmexport bench_frame
func benchFrame() {
	slot := int(frameNo) % liveFrames

	// Small pointer-bearing structs, chained so the whole batch stays
	// reachable from the ring for liveFrames frames.
	var head *node
	kept := make([]*node, 0, smallPerFrame/8+1)
	for i := 0; i < smallPerFrame; i++ {
		n := &node{id: int64(i), frame: frameNo, next: head}
		head = n
		if i%8 == 0 {
			kept = append(kept, n)
		}
	}
	sink = head

	// Medium byte slices, attached to some of the nodes.
	for i := 0; i < medPerFrame; i++ {
		b := make([]byte, 64+(i%4)*64) // 64, 128, 192, 256
		b[0] = byte(i)
		if i < len(kept) {
			kept[i].data = b
		}
	}

	// Larger slices, kept alive in their own ring.
	bigs := make([][]byte, 0, largePerFrame)
	for i := 0; i < largePerFrame; i++ {
		b := make([]byte, 512+(i%3)*512) // 512, 1024, 1536
		b[len(b)-1] = byte(i)
		bigs = append(bigs, b)
	}

	// Touch a slice of the persistent graph each frame (pointer writes,
	// so the write barrier and re-marking stay honest).
	for i := 0; i < 256; i++ {
		g := graph[graphMutIdx]
		g.payload = &node{id: int64(graphMutIdx), frame: frameNo}
		graphMutIdx = (graphMutIdx + 4099) % graphNodes
	}

	ring[slot] = kept
	bigRing[slot] = bigs
	frameNo++
}

//go:wasmexport bench_numgc
func benchNumGC() int32 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int32(ms.NumGC)
}

//go:wasmexport bench_heap_mb
func benchHeapMB() float64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapAlloc) / (1 << 20)
}

func main() {
	buildGraph()
	// Everything happens via exports; park main forever.
	select {}
}
