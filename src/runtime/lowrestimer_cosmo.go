// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// hostHasLowResTimer is osHasLowResTimer for the host the APE runs on.

package runtime

// hostHasLowResTimer is osHasLowResTimer for the host the APE runs on.
//
//go:nosplit
func hostHasLowResTimer() bool {
	return iswindows()
}
