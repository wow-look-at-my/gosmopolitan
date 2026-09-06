// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package runtime

import "internal/goos"

// hostIsDarwin reports whether the kernel under this program is Apple's.
// Every port but cosmo is built for one kernel, so the port answers.
// The cosmo half is in hostos_cosmo.go, where it asks the host.
func hostIsDarwin() bool { return goos.IsDarwin == 1 || goos.IsIos == 1 }

// hostIsLinux reports whether the kernel under this program is Linux.
func hostIsLinux() bool { return goos.IsLinux == 1 || goos.IsAndroid == 1 }
