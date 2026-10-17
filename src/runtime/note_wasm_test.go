// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build (js && wasm && !wasm.threads) || wasip1

package runtime_test

import (
	"runtime"
	"testing"
)

// A note woken while its sleeper is preempted, before the sleeper parks,
// must not ready the sleeper twice or ready a nil g.
func TestNoteWakeupBeforePark(t *testing.T) {
	if !runtime.NoteWakeupBeforePark(-1) {
		t.Error("untimed notetsleepg returned false after a wakeup")
	}
	if !runtime.NoteWakeupBeforePark(10e9) {
		t.Error("timed notetsleepg reported a timeout after a wakeup")
	}
}
