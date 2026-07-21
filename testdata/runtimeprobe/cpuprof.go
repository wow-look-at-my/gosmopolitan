// CPU-profiling check: pprof.StartCPUProfile must deliver real
// samples - native setitimer SIGPROF on Linux hosts, the
// waitable-timer profiler M (NT wave 3 item 3) on Windows hosts.
// Before item 3, StartCPUProfile on NT succeeded and wrote a valid
// profile with ZERO samples; asserting Sample records >= 1 is exactly
// the regression gate for that silent failure. macOS hosts genuinely
// lack SIGPROF delivery (setitimer is not dispatched on darwin - a
// documented wave-2 backlog gap), so the check host-skips there and
// ONLY there, keyed on the HOST TRIPLE, never on an error.

package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"
)

// countPprofSamples walks the uncompressed pprof profile.proto bytes
// just enough to count top-level field-2 (Sample) records - a bounded
// protobuf skim, not a parser, so the probe stays stdlib-only. Every
// step advances i, so the walk terminates on any input.
func countPprofSamples(p []byte) (samples int, errDetail string) {
	i := 0
	readVarint := func() (uint64, bool) {
		var v uint64
		for shift := uint(0); shift < 64; shift += 7 {
			if i >= len(p) {
				return 0, false
			}
			b := p[i]
			i++
			v |= uint64(b&0x7f) << shift
			if b&0x80 == 0 {
				return v, true
			}
		}
		return 0, false // overlong varint
	}
	for i < len(p) {
		tag, tagOK := readVarint()
		if !tagOK {
			return 0, "truncated field tag"
		}
		field, wire := tag>>3, tag&7
		switch wire {
		case 0: // varint
			if _, vOK := readVarint(); !vOK {
				return 0, "truncated varint field"
			}
		case 1: // fixed64
			i += 8
		case 2: // length-delimited
			n, nOK := readVarint()
			if !nOK || n > uint64(len(p)-i) {
				return 0, "truncated length-delimited field"
			}
			if field == 2 {
				samples++
			}
			i += int(n)
		case 5: // fixed32
			i += 4
		default:
			return 0, fmt.Sprintf("unsupported wire type %d", wire)
		}
		if i > len(p) {
			return 0, "field overruns buffer"
		}
	}
	return samples, ""
}

// checkCPUProf profiles ~1.2s of multi-goroutine spinning (one worker
// per P, the preempt-check recipe) and asserts the written profile is
// non-empty, gunzips, and contains at least one Sample record. >=1 is
// deliberate: without HIGH_RESOLUTION timer support NT coalesces the
// 10ms period to ~15.6ms quanta (~64Hz effective), so a rate
// assertion would be runner-dependent; 1.2s yields dozens of ticks
// under either granularity.
func checkCPUProf() {
	if !probeHostIsNT() && !probeHostIsLinux() {
		ok("cpuprof", "skipped (no SIGPROF on this host)")
		return
	}
	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		fail("cpuprof", "StartCPUProfile: %v", err)
		return
	}
	var stop atomic.Uint32
	var spun atomic.Uint64
	var wg sync.WaitGroup
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			x := seed
			for stop.Load() == 0 {
				for j := 0; j < 4096; j++ {
					x = spin(x)
				}
			}
			spun.Add(x)
		}(uint64(i + 1))
	}
	time.Sleep(1200 * time.Millisecond)
	stop.Store(1)
	wg.Wait()
	sink = spun.Load()
	pprof.StopCPUProfile()

	if buf.Len() == 0 {
		fail("cpuprof", "StopCPUProfile wrote an empty profile")
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		fail("cpuprof", "profile is not gzip (%d bytes): %v", buf.Len(), err)
		return
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		fail("cpuprof", "gunzip: %v", err)
		return
	}
	n, detail := countPprofSamples(raw)
	if detail != "" {
		fail("cpuprof", "malformed profile (%d bytes uncompressed): %s", len(raw), detail)
		return
	}
	if n < 1 {
		fail("cpuprof", "no Sample records after 1.2s of spin (%d bytes uncompressed)", len(raw))
		return
	}
	ok("cpuprof", fmt.Sprintf("samples=%d", n))
}
