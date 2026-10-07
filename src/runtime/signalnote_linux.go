// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// signalNote sleeps on a futex word. post makes the FUTEX_WAKE call itself:
// a profiling signal on a thread Go did not create reaches it with little
// nosplit stack left, and futexwakeup's frame does not fit.
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
	futex(unsafe.Pointer(&n.word), _FUTEX_WAKE_PRIVATE, 1, nil, nil, 0)
}

func (n *signalNote) await() {
	for n.word.Load() == 0 {
		futexsleep((*uint32)(unsafe.Pointer(&n.word)), 0, -1)
	}
}
