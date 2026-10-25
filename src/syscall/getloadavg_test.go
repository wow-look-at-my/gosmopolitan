// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin

package syscall

import (
	"errors"
	"math"
	"testing"
)

func TestGetloadavg(t *testing.T) {
	load, err := Getloadavg()
	if err != nil {
		// A Cosmopolitan build on a host without procfs cannot answer this.
		if errors.Is(err, ENOENT) || errors.Is(err, ENOSYS) {
			t.Skipf("this host reports no load average: %v", err)
		}
		t.Fatalf("Getloadavg: %v", err)
	}
	for i, v := range load {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			t.Errorf("loadavg[%d] = %v, want a non-negative finite number", i, v)
		}
	}
}
