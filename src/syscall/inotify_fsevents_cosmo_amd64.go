// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// inotify_fsevents_cosmo_amd64.go holds the inotify fsevents cosmo amd64 code.

package syscall

// fseventNewSource exists so the core links: cosmo.Darwin is false on amd64,
// where no APE starts on a macOS host.
func fseventNewSource(ino *inotifyInstance) (inotifySource, Errno) {
	return nil, ENOSYS
}
