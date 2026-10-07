// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// Operation codes and flags of __ulock_wait and __ulock_wake, from XNU's
// bsd/sys/ulock.h.
const (
	_UL_COMPARE_AND_WAIT = 1
	_ULF_WAKE_ALL        = 0x00000100
	_ULF_NO_ERRNO        = 0x01000000
)

// signalNote sleeps in __ulock_wait on word until a wake stores 1 there and
// calls __ulock_wake, a system call a signal handler may make. Darwin's
// semawakeup takes a pthread mutex, which a handler must not.
type signalNote struct {
	waiting atomic.Uint32
	word    atomic.Uint32
}

func (n *signalNote) reset() {
	n.word.Store(0)
}

//go:nosplit
func (n *signalNote) post() {
	n.word.Store(1)
	ulock_wake(_UL_COMPARE_AND_WAIT|_ULF_WAKE_ALL|_ULF_NO_ERRNO, unsafe.Pointer(&n.word), 0)
}

func (n *signalNote) await() {
	for n.word.Load() == 0 {
		// A return without the word changed is a spurious wakeup or
		// EINTR; the word decides.
		ulock_wait(_UL_COMPARE_AND_WAIT|_ULF_NO_ERRNO, unsafe.Pointer(&n.word), 0, 0)
	}
}
