package main

import "testing"

// TestBgLimit holds bgLimit to its rule: one job per pair of processors, never one job.
func TestBgLimit(t *testing.T) {
	for _, c := range []struct{ numCPU, want int }{{1, 2}, {2, 2}, {3, 2}, {4, 2}, {6, 3}, {8, 4}, {16, 8}} {
		if got := bgLimit(c.numCPU); got != c.want {
			t.Errorf("bgLimit(%d) = %d, want %d", c.numCPU, got, c.want)
		}
	}
}
