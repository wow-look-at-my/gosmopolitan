// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// signalNote sleeps on the waiting M's semaphore on a Linux host, where
// semawakeup is a futex wake.
type signalNote struct {
	waiting   atomic.Uint32
	mp        muintptr // the waiting M
	pipeRead  int32
	pipeWrite int32
}

func (n *signalNote) reset() {
	mp := getg().m
	n.mp.set(mp)
	if !isdarwin() {
		semacreate(mp)
		return
	}
	if n.pipeWrite != 0 {
		return
	}
	r, w, errno := pipe2(_O_CLOEXEC)
	if errno != 0 {
		throw("signalNote: pipe failed")
	}
	const (
		_F_GETFL = 3
		_F_SETFL = 4
	)
	// A full pipe already holds the wakeup, so the write end never needs to block in a signal handler.
	flags, errno := fcntl(w, _F_GETFL, 0)
	if errno != 0 {
		throw("signalNote: fcntl F_GETFL")
	}
	if _, errno := fcntl(w, _F_SETFL, flags|_O_NONBLOCK); errno != 0 {
		throw("signalNote: fcntl F_SETFL")
	}
	n.pipeRead = r
	n.pipeWrite = w
}

//go:nosplit
func (n *signalNote) post() {
	if !isdarwin() {
		semawakeup(n.mp.ptr())
		return
	}
	var b byte
	for write(uintptr(n.pipeWrite), unsafe.Pointer(&b), 1) == -_EINTR {
	}
}

func (n *signalNote) await() {
	if !isdarwin() {
		semasleep(-1)
		return
	}
	var b byte
	for read(n.pipeRead, unsafe.Pointer(&b), 1) == -_EINTR {
	}
}
