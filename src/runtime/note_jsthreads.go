// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime

// sleep and wakeup on one-time events. before any calls to notesleep or
// notewakeup, must call noteclear.
type note struct {
	key uintptr

	gp guintptr
}
