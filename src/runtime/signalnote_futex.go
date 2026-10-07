// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || (js && wasm && wasm.threads)

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// signalNote sleeps on a futex word on every cosmo host and under
// GOWASM=threads. futexwakeup is a system call a signal handler may make.
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
	futexwakeup((*uint32)(unsafe.Pointer(&n.word)), 1)
}

func (n *signalNote) await() {
	for n.word.Load() == 0 {
		futexsleep((*uint32)(unsafe.Pointer(&n.word)), 0, -1)
	}
}

func (n *signalNote) awaitFor(ns int64) bool {
	deadline := nanotime() + ns
	for n.word.Load() == 0 {
		left := deadline - nanotime()
		if left <= 0 {
			return false
		}
		futexsleep((*uint32)(unsafe.Pointer(&n.word)), 0, left)
	}
	return true
}
