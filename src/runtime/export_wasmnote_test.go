// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build (js && wasm && !wasm.threads) || wasip1

package runtime

// NoteWakeupBeforePark sleeps on a note for ns nanoseconds (forever if ns
// is negative) with a preemption request armed, so the sleeper stops in
// the notetsleepg prologue. Another goroutine wakes the note before the
// sleeper parks. It returns what notetsleepg returns.
func NoteWakeupBeforePark(ns int64) bool {
	waitNote := new(note)
	noteclear(waitNote)
	go notewakeup(waitNote)
	gp := getg()
	gp.preempt = true
	gp.stackguard0 = stackPreempt
	return notetsleepg(waitNote, ns)
}
