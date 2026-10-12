// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// The ARM64_NT_CONTEXT layout, built for every cosmo architecture so the
// host-run runtime tests can pin its offsets.

// ntNeon128 is the win64/arm64 NEON128 (several bytes).
type ntNeon128 struct {
	low  uint64
	high int64
}

type ntContextARM64 struct {
	contextFlags uint32
	cpsr         uint32
	x            [31]uint64
	xsp          uint64
	pc           uint64
	v            [32]ntNeon128
	fpcr         uint32
	fpsr         uint32
	bcr          [8]uint32
	bvr          [8]uint64
	wcr          [2]uint32
	wvr          [2]uint64
}
