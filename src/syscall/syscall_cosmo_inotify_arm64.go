// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package syscall

// The arm64 table has no inotify_init number. The linux port builds the name
// on inotify_init1 with no flags, and so does this.

func InotifyInit() (fd int, err error) {
	return InotifyInit1(0)
}
