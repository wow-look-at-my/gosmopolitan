// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package unix

// KernelVersion returns the major and minor version of the kernel.
func KernelVersion() (major int, minor int) {
	return 5, 10
}
