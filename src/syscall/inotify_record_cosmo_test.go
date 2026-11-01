// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"bytes"
	"runtime"
	"syscall"
	"testing"
)

// TestInotifyRecordStaysInItsAllocation fills the holes between live blocks of
// a record's size class with nameless records. A record that writes past its
// end lands on the live block after it.
func TestInotifyRecordStaysInItsAllocation(t *testing.T) {
	const pairs = 4096
	filled := bytes.Repeat([]byte{0xff}, syscall.SizeofInotifyEvent)
	live := make([][]byte, 0, pairs)
	holes := make([][]byte, 0, pairs)
	for range pairs {
		block := make([]byte, syscall.SizeofInotifyEvent)
		copy(block, filled)
		live = append(live, block)
		holes = append(holes, make([]byte, syscall.SizeofInotifyEvent))
	}
	clear(holes)
	runtime.GC()
	runtime.GC()

	var queue syscall.InotifyQueueForTest
	for idx := range pairs {
		queue.PushForTest(int32(idx+1), syscall.IN_IGNORED, 0, "")
	}
	for idx, block := range live {
		if !bytes.Equal(block, filled) {
			t.Fatalf("live block %d holds %x", idx, block)
		}
	}
	runtime.KeepAlive(&queue)
}
