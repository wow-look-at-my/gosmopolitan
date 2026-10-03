// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package cosmo_test

import (
	"internal/runtime/syscall/cosmo"
	"testing"
)

// sigA2LTab is indexed from sigactionTramp's assembly, so it is a
// second copy of the correspondence darwinXlatSignalA2L holds. This
// pins the two together.
func TestSigA2LTab(t *testing.T) {
	tab := cosmo.SigA2LTab
	for a := uintptr(0); a < uintptr(len(tab)); a++ {
		want := uintptr(0)
		if l, ok := cosmo.DarwinXlatSignalA2L(a); ok {
			want = l
		}
		if got := uintptr(tab[a]); got != want {
			t.Errorf("sigA2LTab[%d] = %d, want %d", a, got, want)
		}
	}
}

// Apple's KERNEL struct sigaction, what __sigaction reads. Layout from
// Go's pre-1.12 darwin port (go1.8 runtime/defs_darwin_amd64.go).
func TestXnuKsigactionLayout(t *testing.T) {
	size, handler, tramp, mask, flags := cosmo.XnuKsigactionLayout()
	for _, f := range []struct {
		name      string
		got, want uintptr
	}{
		{"size", size, 24},
		{"sa_handler", handler, 0},
		{"sa_tramp", tramp, 8},
		{"sa_mask", mask, 16},
		{"sa_flags", flags, 20},
	} {
		if f.got != f.want {
			t.Errorf("xnuKsigactiont %s = %d, want %d", f.name, f.got, f.want)
		}
	}
}

// Apple's user64_sigaction, the OLD action __sigaction copies out
// (XNU kern_sig.c sigaction_kern_to_user64): no sa_tramp, 16 bytes.
func TestXnuSigactionLayout(t *testing.T) {
	size, handler, mask, flags := cosmo.XnuSigactionLayout()
	for _, f := range []struct {
		name      string
		got, want uintptr
	}{
		{"size", size, 16},
		{"sa_handler", handler, 0},
		{"sa_mask", mask, 8},
		{"sa_flags", flags, 12},
	} {
		if f.got != f.want {
			t.Errorf("xnuSigactiont %s = %d, want %d", f.name, f.got, f.want)
		}
	}
}
