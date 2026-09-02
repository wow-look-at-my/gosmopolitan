// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import "unsafe"

// Exports for ctx_cosmo_nt_test.go: the ARM64_NT_CONTEXT offsets
// sys_cosmo_nt_arm64.s and os_cosmo_nt_ctx_arm64.go rely on. No Linux
// or macOS host executes that code, so the layout is only ever checked
// by pinning it.

const (
	NTContextARM64Size  = unsafe.Sizeof(ntContextARM64{})
	NTContextARM64X28   = unsafe.Offsetof(ntContextARM64{}.x) + 28*unsafe.Sizeof(uint64(0))
	NTContextARM64Sp    = unsafe.Offsetof(ntContextARM64{}.xsp)
	NTContextARM64Pc    = unsafe.Offsetof(ntContextARM64{}.pc)
	NTContextARM64Fpcr  = unsafe.Offsetof(ntContextARM64{}.fpcr)
	NTExceptionPtrsCtx  = unsafe.Offsetof(ntExceptionPointers{}.context)
	NTExceptionPtrsSize = unsafe.Sizeof(ntExceptionPointers{})
)
