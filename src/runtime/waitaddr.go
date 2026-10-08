// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import (
	"internal/cpu"
	"internal/runtime/atomic"
	"unsafe"
)

// An M that waits for a 32-bit word in the runtime to change sleeps in the
// OS on its waitAddrNote, and the code that changes the word wakes it. This
// is how an M waits for a goroutine's status to leave a state another M
// holds it in, and for a goroutine profile record another M is writing.
//
// Sleeping Ms hang off the bucket of waitAddrTable that the word's address
// selects. A sleeper counts itself into the bucket before it reads the word
// for the last time, and a writer reads the count after it stores the word.
// Go atomics are sequentially consistent, so either the sleeper reads the
// new value or the writer sees the sleeper and wakes it.
//
// A wakeup does not promise the word changed: the bucket is shared, and a
// sleeper re-reads the word and sleeps again if it still holds the old
// value.
//
// waitAddrWake takes the bucket lock, a runtime mutex, so a signal handler
// must not call it.

const waitAddrBuckets = 61

type waitAddrBucket struct {
	lock    mutex
	waiters atomic.Uint32 // number of Ms linked from head
	head    muintptr      // Ms sleeping on a word that hashes here, linked through m.waitAddrNext
}

var waitAddrTable [waitAddrBuckets]struct {
	waitAddrBucket
	pad [cpu.CacheLinePadSize - unsafe.Sizeof(waitAddrBucket{})%cpu.CacheLinePadSize]byte
}

//go:nosplit
func waitAddrBucketOf(addr *uint32) *waitAddrBucket {
	return &waitAddrTable[(uintptr(unsafe.Pointer(addr))>>3)%waitAddrBuckets].waitAddrBucket
}

// waitAddrSleep puts the M to sleep until *addr no longer holds seen, or
// until ns nanoseconds have passed when ns >= 0. It may return early; the
// caller re-reads *addr.
//
//go:systemstack
func waitAddrSleep(addr *uint32, seen uint32, ns int64) {
	mp := getg().m
	bucket := waitAddrBucketOf(addr)
	noteclear(&mp.waitAddrNote)

	lock(&bucket.lock)
	mp.waitAddr = uintptr(unsafe.Pointer(addr))
	mp.waitAddrNext = bucket.head
	bucket.head.set(mp)
	bucket.waiters.Add(1)
	if atomic.Load(addr) != seen {
		waitAddrUnlink(bucket, mp)
		unlock(&bucket.lock)
		return
	}
	unlock(&bucket.lock)

	if ns < 0 {
		notesleep(&mp.waitAddrNote)
		return
	}
	if notetsleep(&mp.waitAddrNote, ns) {
		return
	}

	// The sleep timed out. A writer that has already unlinked this M is
	// about to wake it, and that wakeup is consumed here so that it does
	// not land on the note's next use.
	lock(&bucket.lock)
	linked := mp.waitAddr != 0
	if linked {
		waitAddrUnlink(bucket, mp)
	}
	unlock(&bucket.lock)
	if !linked {
		notesleep(&mp.waitAddrNote)
	}
}

// waitAddrUnlink removes mp from bucket. The caller holds bucket.lock.
func waitAddrUnlink(bucket *waitAddrBucket, mp *m) {
	link := &bucket.head
	for link.ptr() != mp {
		if link.ptr() == nil {
			throw("waitAddrUnlink: M not in bucket")
		}
		link = &link.ptr().waitAddrNext
	}
	*link = mp.waitAddrNext
	mp.waitAddrNext = 0
	mp.waitAddr = 0
	bucket.waiters.Add(-1)
}

// waitAddrWake wakes every M sleeping in waitAddrSleep on addr. The caller
// calls it after storing the new value of *addr.
//
//go:nosplit
func waitAddrWake(addr *uint32) {
	if waitAddrBucketOf(addr).waiters.Load() == 0 {
		return
	}
	systemstack(func() {
		waitAddrWakeSlow(addr)
	})
}

func waitAddrWakeSlow(addr *uint32) {
	bucket := waitAddrBucketOf(addr)
	key := uintptr(unsafe.Pointer(addr))
	var woken muintptr
	lock(&bucket.lock)
	link := &bucket.head
	for link.ptr() != nil {
		mp := link.ptr()
		if mp.waitAddr != key {
			link = &mp.waitAddrNext
			continue
		}
		*link = mp.waitAddrNext
		mp.waitAddr = 0
		bucket.waiters.Add(-1)
		mp.waitAddrNext = woken
		woken.set(mp)
	}
	unlock(&bucket.lock)

	// An unlinked M sleeps until its note is woken, so nothing else touches
	// its waitAddrNext until the notewakeup below.
	for woken != 0 {
		mp := woken.ptr()
		woken = mp.waitAddrNext
		mp.waitAddrNext = 0
		notewakeup(&mp.waitAddrNote)
	}
}

// gStatusWord is the address of gp's status word, for waitAddrSleep and
// waitAddrWake.
//
//go:nosplit
func gStatusWord(gp *g) *uint32 {
	return (*uint32)(unsafe.Pointer(&gp.atomicstatus))
}

// gStatusChanged wakes the Ms waiting for gp's status to change. Every
// transition of gp's status calls it after the new status is stored.
//
//go:nosplit
func gStatusChanged(gp *g) {
	waitAddrWake(gStatusWord(gp))
}

// gStatusWait puts the M to sleep until gp's status is no longer seen, or
// until ns nanoseconds have passed when ns >= 0.
//
//go:nosplit
func gStatusWait(gp *g, seen uint32, ns int64) {
	systemstack(func() {
		waitAddrSleep(gStatusWord(gp), seen, ns)
	})
}
