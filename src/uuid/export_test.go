// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package uuid

// ResetV7ForTesting forgets the timestamp NewV7 handed out last.
func ResetV7ForTesting() {
	v7mu.Lock()
	defer v7mu.Unlock()
	v7lastSecs = 0
	v7lastTimestamp = 0
}
