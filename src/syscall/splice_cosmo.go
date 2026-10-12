// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

// Splice exists here for source compatibility.
func Splice(rfd int, roff *int64, wfd int, woff *int64, len int, flags int) (n int64, err error) {
	return 0, ENOSYS
}
