// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime_test

import (
	. "runtime"
	"testing"
)

// TestCosmoDarwinFutexDelay covers the arithmetic in one step of the
// darwin futex wait (os_cosmo.go). XNU has no futex, so the wait is a
// poll of the word with a backoff, and this decides each sleep.
//
// Two properties matter, and neither can be observed from a Linux host
// by running the poll loop. A sleep must never run past the caller's
// deadline, or a timed lock2 overshoots its timeout. And a remaining
// time under one microsecond must not round down to a zero-length
// sleep, which would turn the wait into a spin on the CPU.
func TestCosmoDarwinFutexDelay(t *testing.T) {
	for _, c := range []struct {
		name    string
		sleep   uint32
		left    int64
		timed   bool
		want    uint32
		expired bool
	}{
		{"untimed passes the backoff through", 20, 0, false, 20, false},
		{"untimed ignores leftNsec", 5000, -1, false, 5000, false},
		{"deadline in the past", 20, 0, true, 0, true},
		{"deadline already missed", 20, -1000, true, 0, true},
		{"plenty of time left", 20, 1_000_000, true, 20, false},
		{"exactly enough time left", 20, 20_000, true, 20, false},
		{"clamped to what is left", 5000, 300_000, true, 300, false},
		{"sub-microsecond remainder never sleeps zero", 5000, 999, true, 1, false},
		{"one nanosecond left still sleeps", 20, 1, true, 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, expired := DarwinFutexDelay(c.sleep, c.left, c.timed)
			if expired != c.expired {
				t.Fatalf("expired = %v, want %v", expired, c.expired)
			}
			if expired {
				return
			}
			if got != c.want {
				t.Errorf("usec = %d, want %d", got, c.want)
			}
			if got == 0 {
				t.Error("usec = 0: a zero-length sleep spins the CPU")
			}
			// The two properties collide below a microsecond: the floor
			// that stops a zero-length sleep is itself an overshoot.
			// One microsecond is the whole error, and it buys a wait
			// that sleeps instead of spinning, so the bound allows the
			// floor and nothing beyond it.
			if c.timed && int64(got)*1000 > c.left && got != 1 {
				t.Errorf("usec = %d overshoots the %dns left", got, c.left)
			}
		})
	}
}
