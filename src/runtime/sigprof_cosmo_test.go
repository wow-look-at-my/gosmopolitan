// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime_test

import (
	. "runtime"
	"testing"
)

func TestCosmoXnuItimervalABI(t *testing.T) {
	if XnuTimevalSize != 16 {
		t.Errorf("xnuTimeval size = %d, want 16", XnuTimevalSize)
	}
	if XnuItimervalSize != 32 {
		t.Errorf("xnuItimerval size = %d, want 32", XnuItimervalSize)
	}
	if XnuItimervalUsec1Off != 8 {
		t.Errorf("it_interval.tv_usec offset = %d, want 8", XnuItimervalUsec1Off)
	}
	if XnuItimervalUsec2Off != 24 {
		t.Errorf("it_value.tv_usec offset = %d, want 24", XnuItimervalUsec2Off)
	}
}

// TestCosmoTimevalTranslation exercises the Linux<->Apple timeval
// translation darwinSetitimer applies in both directions.
func TestCosmoTimevalTranslation(t *testing.T) {
	if sec, usec := CosmoTimevalL2X(0, 10000); sec != 0 || usec != 10000 {
		t.Errorf("L2X(0, 10000) = (%d, %d), want (0, 10000)", sec, usec)
	}
	// The disarm shape: hz=0 -> zero itimerval.
	if sec, usec := CosmoTimevalL2X(0, 0); sec != 0 || usec != 0 {
		t.Errorf("L2X(0, 0) = (%d, %d), want (0, 0)", sec, usec)
	}
	if sec, usec := CosmoTimevalX2L(0, 0); sec != 0 || usec != 0 {
		t.Errorf("X2L(0, 0) = (%d, %d), want (0, 0)", sec, usec)
	}
	// Largest legal usec plus nonzero seconds, round-tripped.
	xsec, xusec := CosmoTimevalL2X(3, 999999)
	if xsec != 3 || xusec != 999999 {
		t.Errorf("L2X(3, 999999) = (%d, %d), want (3, 999999)", xsec, xusec)
	}
	if sec, usec := CosmoTimevalX2L(xsec, xusec); sec != 3 || usec != 999999 {
		t.Errorf("X2L round-trip = (%d, %d), want (3, 999999)", sec, usec)
	}
}
