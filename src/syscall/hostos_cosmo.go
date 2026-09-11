// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "runtime"

// cosmoHostIsLinux reports whether this process runs on a Linux host.
// One APE runs on three of them, and runtime.GOOS names the one it
// landed on. The APE entry stub records it before any Go code runs.
func cosmoHostIsLinux() bool { return runtime.GOOS == "linux" }
