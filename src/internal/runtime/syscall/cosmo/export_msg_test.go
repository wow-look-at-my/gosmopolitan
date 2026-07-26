// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// Exports for socket_msg_cosmo_test.go. CmsgToApple/CmsgToLinux/
// XlatMsgFlags are exported for package syscall already; only the
// msghdr mirror types need a test window.

type LinuxMsghdrForTest = linuxMsghdr
type AppleMsghdrForTest = appleMsghdr
