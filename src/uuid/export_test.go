// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

// ResetV7ForTesting forgets the timestamp NewV7 handed out last.
//
// A synctest bubble starts its clock at one fixed instant, so two bubbles ask
// for the same millisecond. The second one reads that millisecond as a repeat
// and takes the monotonic bump, which is not the time the caller asserts on.
func ResetV7ForTesting() {
	v7mu.Lock()
	defer v7mu.Unlock()
	v7lastSecs = 0
	v7lastTimestamp = 0
}
