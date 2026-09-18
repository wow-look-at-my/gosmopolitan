// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package syscall

// The program counter accessors the linux port declares per architecture, in
// syscall_linux_amd64.go. A caller uses them to stay architecture-neutral
// over a PtraceRegs it got from PtraceGetRegs.

func (r *PtraceRegs) PC() uint64 { return r.Rip }

func (r *PtraceRegs) SetPC(pc uint64) { r.Rip = pc }
