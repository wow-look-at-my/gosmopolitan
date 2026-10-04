// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo

package runtime

import "internal/goos"

//go:nosplit
func hostIsDarwin() bool { return goos.IsDarwin == 1 || goos.IsIos == 1 }

//go:nosplit
func hostIsLinux() bool { return goos.IsLinux == 1 || goos.IsAndroid == 1 }

//go:nosplit
func hostIsWindows() bool { return goos.IsWindows == 1 }
