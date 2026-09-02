// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime_test

import (
	"runtime"
	"testing"
)

// TestCosmoSigNTContextLayout pins the ARM64_NT_CONTEXT offsets the
// arm64 exception thunk reads (x28 for g, pc for the Go-text check) and
// the EXCEPTION_POINTERS slot it dereferences to reach the CONTEXT.
// Values are winnt.h's ARM64_NT_CONTEXT.
func TestCosmoSigNTContextLayout(t *testing.T) {
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"ARM64_NT_CONTEXT.X[28]", runtime.NTContextARM64X28, 232},
		{"ARM64_NT_CONTEXT.Sp", runtime.NTContextARM64Sp, 256},
		{"ARM64_NT_CONTEXT.Pc", runtime.NTContextARM64Pc, 264},
		{"ARM64_NT_CONTEXT.Fpcr", runtime.NTContextARM64Fpcr, 784},
		{"sizeof(ARM64_NT_CONTEXT)", runtime.NTContextARM64Size, 912},
		{"EXCEPTION_POINTERS.ContextRecord", runtime.NTExceptionPtrsCtx, 8},
		{"sizeof(EXCEPTION_POINTERS)", runtime.NTExceptionPtrsSize, 16},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: offset/size %d, want %d", c.name, c.got, c.want)
		}
	}
}
