// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "runtime"

// cosmoHostIsLinux reports whether this process runs on a Linux host.
func cosmoHostIsLinux() bool { return runtime.GOOS == "linux" }
