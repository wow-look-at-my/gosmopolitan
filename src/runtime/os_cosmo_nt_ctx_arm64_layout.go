// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// The ARM64_NT_CONTEXT layout, built for every cosmo architecture so
// the host-run runtime tests can pin its offsets. Only the arm64 port
// uses it (os_cosmo_nt_ctx_arm64.go aliases ntContext to it); on amd64
// it is a dead type. Layout comes from upstream
// internal/runtime/syscall/windows/defs_windows_arm64.go.

// ntNeon128 is the win64/arm64 NEON128 (16 bytes).
type ntNeon128 struct {
	low  uint64
	high int64
}

// ntContextARM64 is ARM64_NT_CONTEXT (912 bytes). x[28] is the g
// register, xsp is at 256 and pc at 264; sys_cosmo_nt_arm64.s reads x[28]
// and pc through the go_asm.h offsets of this type.
type ntContextARM64 struct {
	contextFlags uint32
	cpsr         uint32
	x            [31]uint64 // fp is x[29], lr is x[30]
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
