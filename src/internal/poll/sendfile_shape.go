// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo || linux || android

package poll

// sendfileIsLinuxShaped says the platform's sendfile reads from the
// input file's own position and advances it, so a caller passes no
// offset and does no seek of its own.
//
// This is a question about the PORT, not the host: a cosmo binary
// answers true on macOS and on Windows too. Apple's sendfile and the NT
// emulation both present the Linux shape, because the syscall layer
// converts each of them. runtime.GOOS names the host there, so it is
// the wrong thing to ask.
const sendfileIsLinuxShaped = true
