//go:build !js

package main

// The native build runs the same kernel on the same generated frame, so the
// wasm number has a ceiling to be read against. That ratio is the point of the
// benchmark: the kernel is fixed, and what moves is the code the toolchain
// generates for it.

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"
)

func main() {
	cells := flag.Int("cells", 283326, "grid cells to aggregate into")
	ppc := flag.Int("pages-per-cell", 16, "pages per cell")
	iters := flag.Int("iters", 50, "measured passes")
	capture := flag.String("capture", "", "optional path to a captured MV v4 keyframe to use instead of the generated frame")
	flag.Parse()

	var (
		states []byte
		tbl    *VMATable
		total  int
	)
	if *capture != "" {
		raw, err := os.ReadFile(*capture)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read capture:", err)
			os.Exit(1)
		}
		states, tbl, total, err = ParseKeyframe(raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "parse capture:", err)
			os.Exit(1)
		}
	} else {
		states, tbl, total = SyntheticFrame(DefaultShape, 0x9E3779B97F4A7C15)
	}
	mark, _, _ := SyntheticFrame(FrameShape{
		TotalPages: total, VMAs: DefaultShape.VMAs,
		BusyPct: 2, AvgGap: DefaultShape.AvgGap * 2, AvgRun: DefaultShape.AvgRun,
	}, 0xD1B54A32D192ED03)
	if len(mark) > len(states) {
		mark = mark[:len(states)]
	}

	cellBuf := make([]byte, *cells*4+4096)
	encoded := EncodeKeyframePages(states)

	busy, runs := 0, 0
	for p := 0; p < total; {
		q := p + 1
		for q < total && states[q] == states[p] {
			q++
		}
		if states[p] != 0 {
			busy += q - p
		}
		runs++
		p = q
	}
	fmt.Printf("frame: %d pages, %d VMAs, %d runs, %.1f%% busy, %d cells @ %d pages\n",
		total, len(tbl.PageOff), runs, 100*float64(busy)/float64(total), *cells, *ppc)

	bench("AggregateInto (no mark)", *iters, func() {
		AggregateInto(cellBuf, states, tbl, 0, total, *cells, *ppc, nil)
	})
	bench("AggregateInto (mark set)", *iters, func() {
		AggregateInto(cellBuf, states, tbl, 0, total, *cells, *ppc, mark)
	})
	bench("DecodeKeyframePages", *iters, func() {
		DecodeKeyframePages(encoded, total)
	})

	AggregateInto(cellBuf, states, tbl, 0, total, *cells, *ppc, nil)
	var h uint32 = 2166136261
	for _, b := range cellBuf[:*cells*4] {
		h = (h ^ uint32(b)) * 16777619
	}
	fmt.Printf("checksum: %d\n", h&0x7FFFFFFF)
}

// bench reports the median of iters passes, and the min, which is the least
// noise-prone estimator for CPU-bound work.
func bench(name string, iters int, fn func()) {
	fn()
	fn()
	ts := make([]float64, 0, iters)
	for i := 0; i < iters; i++ {
		s := time.Now()
		fn()
		ts = append(ts, float64(time.Since(s).Microseconds())/1000)
	}
	sort.Float64s(ts)
	fmt.Printf("%-28s %8.2f ms   (min %.2f, max %.2f)\n",
		name, ts[len(ts)/2], ts[0], ts[len(ts)-1])
}
