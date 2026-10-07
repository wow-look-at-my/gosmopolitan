// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import "internal/runtime/atomic"

// signalNote never sleeps on wasm. Its waiters wait for a signal handler,
// for a profiling signal or a preemption signal in flight to Darwin, and
// wasm has neither.
type signalNote struct {
	waiting atomic.Uint32
}

func (n *signalNote) reset() {}

//go:nosplit
func (n *signalNote) post() {
	throw("signalNote: wake on wasm")
}

func (n *signalNote) await() {
	throw("signalNote: sleep on wasm")
}
