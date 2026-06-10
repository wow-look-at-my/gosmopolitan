// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package unix

// Cosmopolitan uses Linux syscall numbers.
// ARM64 uses the "generic" Linux syscall numbers.
const (
	getrandomTrap       uintptr = 278
	copyFileRangeTrap   uintptr = 285
	pidfdSendSignalTrap uintptr = 424
	pidfdOpenTrap       uintptr = 434
	openat2Trap         uintptr = 437
)
