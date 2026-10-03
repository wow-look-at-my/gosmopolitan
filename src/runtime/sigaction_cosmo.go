// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// This version is used on Cosmopolitan systems.
// We don't use cgo, so we call the syscall directly.

//go:nosplit
//go:nowritebarrierrec
func sigaction(sig uint32, new, old *sigactiont) {
	sysSigaction(sig, new, old)
}
