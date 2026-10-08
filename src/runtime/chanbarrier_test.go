// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

// A send on an unbuffered channel to a parked receiver copies the value
// straight onto the receiver's stack, which the GC may already have
// scanned. During a concurrent mark that copy must go through the write
// barrier, or the only reference to the value sits where the GC will not
// look again and the value is freed while still in use (go.dev/issue/11643,
// where select's send path skipped it). These tests send during a mark
// phase and check that the value reached the write barrier buffer.

func TestChanSendSelectBarrier(t *testing.T) {
	testChanSendBarrier(t, true)
}

func TestChanSendBarrier(t *testing.T) {
	testChanSendBarrier(t, false)
}

// chanBarrierSends is how many separate sends each test checks.
const chanBarrierSends = 8

func testChanSendBarrier(t *testing.T, useSelect bool) {
	for range chanBarrierSends {
		ch := make(chan *runtime.ChanBarrierValue)
		received := make(chan *runtime.ChanBarrierValue)
		go func() { received <- <-ch }()
		for !runtime.ChanRecvWaiting(ch) {
			runtime.Gosched()
		}
		val := &runtime.ChanBarrierValue{Err: errors.New("sent")}

		if !runtime.GCMarksConcurrently() {
			// No concurrent mark phase exists for the copy to race with.
			ch <- val
			if got := <-received; got != val {
				t.Fatalf("received %p, sent %p", got, val)
			}
			continue
		}

		sent, shaded := sendDuringMark(t, ch, val, useSelect)
		if !sent {
			t.Fatal("no GC cycle reached its concurrent mark phase")
		}
		if got := <-received; got != val {
			t.Fatalf("received %p, sent %p", got, val)
		}
		if !shaded {
			t.Fatalf("a send to a parked receiver during the mark phase (select=%v) did not pass the value through the write barrier", useSelect)
		}
	}
}

// sendDuringMark runs GC cycles until the send happens inside a mark phase.
func sendDuringMark(t *testing.T, ch chan *runtime.ChanBarrierValue, val *runtime.ChanBarrierValue, useSelect bool) (sent, shaded bool) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				runtime.GC()
			}
		}
	}()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if sent, shaded = runtime.ChanSendShades(ch, val, useSelect); sent {
			return sent, shaded
		}
		runtime.Gosched()
	}
	t.Logf("gave up after %v without seeing a mark phase", time.Minute)
	return false, false
}
