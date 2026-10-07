// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !(js && wasm.threads)

package runtime

import "internal/runtime/atomic"

// signalNote never sleeps on single-threaded wasm, where no other thread
// could wake it. A wake that the waiter's own code posts between arm and
// disarm leaves word set, and await returns at once.
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
}

func (n *signalNote) await() {
	if n.word.Load() == 0 {
		throw("signalNote: sleep with no other thread to wake it")
	}
}

func (n *signalNote) awaitFor(ns int64) bool {
	n.await()
	return true
}
