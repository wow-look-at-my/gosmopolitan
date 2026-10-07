// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo && !darwin

package runtime

// sigSafeEvent exists only where usesSigNote can report true. Notes are
// async-signal-safe on every other port, so its methods are never called.
type sigSafeEvent struct{}

// sigSafeEventNeeded reports whether a note cannot be woken from a
// signal handler on this host, so that a wait a handler ends must use a
// sigSafeEvent.
func sigSafeEventNeeded() bool {
	return false
}

func (e *sigSafeEvent) init() bool {
	return false
}

func (e *sigSafeEvent) wake() {
	throw("sigSafeEvent.wake")
}

func (e *sigSafeEvent) sleep(ns int64) bool {
	throw("sigSafeEvent.sleep")
	return false
}
