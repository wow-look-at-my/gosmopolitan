// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Export the write barrier of a direct channel send for testing.

package runtime

import "unsafe"

// ChanBarrierValue is what the chan barrier tests send.
type ChanBarrierValue struct {
	Err error
}

// GCMarksConcurrently reports whether a GC cycle has a concurrent mark
// phase. Under GODEBUG=gcstoptheworld it marks with the world stopped.
func GCMarksConcurrently() bool {
	return debug.gcstoptheworld == 0
}

// ChanRecvWaiting reports whether a receiver is parked on ch.
func ChanRecvWaiting(ch chan *ChanBarrierValue) bool {
	c := *(**hchan)(unsafe.Pointer(&ch))
	lock(&c.lock)
	waiting := c.recvq.first != nil
	unlock(&c.lock)
	return waiting
}

// ChanSendShades sends val to the receiver parked on ch, if a GC cycle is
// in its concurrent mark phase, and reports whether it sent. A send runs
// the receiver's copy of val through the write barrier, and shaded reports
// whether val reached this P's write barrier buffer. The M holds a lock
// from the check to the read, so the mark phase cannot end and nothing
// flushes the buffer in between. With useSelect the send goes through
// selectgo, otherwise through chansend.
func ChanSendShades(ch chan *ChanBarrierValue, val *ChanBarrierValue, useSelect bool) (sent, shaded bool) {
	mp := acquirem()
	defer releasem(mp)
	if gcphase != _GCmark || !writeBarrier.enabled {
		return false, false
	}
	// A send runs a few more barriers after the copy. Starting from an empty
	// buffer, none of them fills it, so no flush takes val back out.
	buf := &mp.p.ptr().wbBuf
	if !buf.empty() {
		wbBufFlush()
	}
	if useSelect {
		var never chan struct{} // nil: never ready, and a second case makes this a selectgo
		select {
		case ch <- val:
		case <-never:
		}
	} else {
		ch <- val
	}
	want := uintptr(unsafe.Pointer(val))
	for slot := uintptr(unsafe.Pointer(&buf.buf[0])); slot < buf.next; slot += unsafe.Sizeof(uintptr(0)) {
		if *(*uintptr)(unsafe.Pointer(slot)) == want {
			return true, true
		}
	}
	return true, false
}
