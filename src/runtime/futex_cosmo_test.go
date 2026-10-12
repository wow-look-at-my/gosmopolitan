// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime_test

import (
	. "runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestCosmoXnuUlockTimeout pins the conversion of a futexsleep timeout into
// __ulock_wait's microseconds, where zero means no timeout. A finite wait
// must never become zero, or it would sleep forever.
func TestCosmoXnuUlockTimeout(t *testing.T) {
	for _, c := range []struct {
		name string
		nsec int64
		want uint32
	}{
		{"forever", -1, 0},
		{"zero still times out", 0, 1},
		{"sub-microsecond rounds up", 1, 1},
		{"exact microseconds", 5000, 5},
		{"partial microsecond rounds up", 5001, 6},
		{"saturates", 1 << 62, 1<<32 - 1},
	} {
		if got := XnuUlockTimeout(c.nsec); got != c.want {
			t.Errorf("%s: XnuUlockTimeout(%d) = %d, want %d", c.name, c.nsec, got, c.want)
		}
	}
}

// TestCosmoFutexWakesSleeper pins that futexsleep sleeps in the host kernel
// and that futexwakeup ends the sleep. The word never changes, so a sleeper
// that polled the word would sleep out its whole timeout instead.
func TestCosmoFutexWakesSleeper(t *testing.T) {
	var word uint32
	var asleep atomic.Bool
	done := make(chan struct{})
	go func() {
		asleep.Store(true)
		CosmoFutexsleep(&word, 0, int64(time.Minute))
		close(done)
	}()
	deadline := time.After(30 * time.Second)
	// A wake that lands before the sleeper enters the kernel wakes nobody, so
	// the waker repeats until the sleeper is out.
	for {
		if asleep.Load() {
			CosmoFutexwakeup(&word, 1)
		}
		select {
		case <-done:
			return
		case <-deadline:
			t.Fatal("futexwakeup did not end futexsleep")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestCosmoFutexsleepTimesOut pins that a timed futexsleep returns with no
// wake, and that it returns at once when the word already differs.
func TestCosmoFutexsleepTimesOut(t *testing.T) {
	var word uint32
	start := time.Now()
	CosmoFutexsleep(&word, 0, int64(20*time.Millisecond))
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("a 20ms futexsleep took %v", elapsed)
	}
	start = time.Now()
	CosmoFutexsleep(&word, 1, int64(time.Minute))
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("futexsleep on a word that differs took %v", elapsed)
	}
}
