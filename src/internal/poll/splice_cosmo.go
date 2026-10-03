// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package poll

// Splice is not supported on cosmo - fall back to regular I/O.
func Splice(dst, src *FD, remain int64) (written int64, handled bool, err error) {
	return 0, false, nil
}
