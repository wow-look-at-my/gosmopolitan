// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// SweepClaim stands in for a sweeper that owns the span of a heap
// object: ClaimSweptSpan moves the span from swept to being swept, and
// Publish is that sweeper's release.
type SweepClaim struct {
	span *mspan
	gen  uint32
}

// ClaimSweptSpan marks the span holding ptr as being swept. The span
// must be swept and no GC may run until Publish.
func ClaimSweptSpan(ptr unsafe.Pointer) SweepClaim {
	span := spanOfHeap(uintptr(ptr))
	if span == nil {
		panic("ClaimSweptSpan: not a heap object")
	}
	pinned := acquirem()
	gen := mheap_.sweepgen
	claimed := atomic.Cas(&span.sweepgen, gen, gen-1)
	releasem(pinned)
	if !claimed {
		panic("ClaimSweptSpan: span is not swept")
	}
	return SweepClaim{span, gen}
}

// EnsureSwept calls ensureSwept on the claimed span the way its callers
// do, non-preemptibly.
func (claim SweepClaim) EnsureSwept() {
	pinned := acquirem()
	claim.span.ensureSwept()
	releasem(pinned)
}

// Publish marks the claimed span swept again, the way sweep does.
func (claim SweepClaim) Publish() {
	claim.span.publishSwept(claim.gen)
}

// SpanSweepWaiters returns the number of Ms asleep in ensureSwept.
func SpanSweepWaiters() uint32 {
	return sweep.spanWaiters.count.Load()
}

// HeapStatsReadSleepsOnWriter holds a heap-stats write section open on
// the calling goroutine's P while another goroutine reads the stats. It
// reports whether the reader marked that P as waited on and stayed
// blocked until the section closed. It gives up after timeout
// nanoseconds without the mark.
func HeapStatsReadSleepsOnWriter(timeout int64) bool {
	phase := new(struct {
		writing, finished atomic.Uint32
	})
	done := make(chan struct{})
	go func() {
		for phase.writing.Load() == 0 {
			usleep(100)
		}
		metricsLock()
		var out heapStatsDelta
		memstats.heapStats.read(&out)
		metricsUnlock()
		phase.finished.Store(1)
		close(done)
	}()

	pinned := acquirem()
	proc := pinned.p.ptr()
	memstats.heapStats.acquire()
	phase.writing.Store(1)
	deadline := nanotime() + timeout
	for proc.statsSeq.Load()&statsSeqReaderWaiting == 0 && nanotime() < deadline {
		usleep(100)
	}
	marked := proc.statsSeq.Load()&statsSeqReaderWaiting != 0
	readEarly := phase.finished.Load() != 0
	memstats.heapStats.release()
	releasem(pinned)
	<-done
	return marked && !readEarly
}
