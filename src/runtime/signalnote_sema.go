// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !wasm && !darwin && !(cosmo && arm64)

package runtime

import "internal/runtime/atomic"

// signalNote sleeps on the waiting M's semaphore. semawakeup is a futex
// wake, an lwp unpark, a sem_post or an event set here, each a system call
// a signal handler may make.
type signalNote struct {
	waiting atomic.Uint32
	mp      muintptr // the waiting M
}

func (n *signalNote) reset() {
	mp := getg().m
	semacreate(mp)
	n.mp.set(mp)
}

//go:nosplit
func (n *signalNote) post() {
	semawakeup(n.mp.ptr())
}

func (n *signalNote) await() {
	semasleep(-1)
}
