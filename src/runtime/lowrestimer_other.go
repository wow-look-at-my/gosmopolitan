// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo

package runtime

// hostHasLowResTimer reports whether the host timer has a 1ms or coarser
// resolution.
//
//go:nosplit
func hostHasLowResTimer() bool {
	return osHasLowResTimer
}
