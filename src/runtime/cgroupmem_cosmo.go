// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// cgroupHostSupported reports whether this host has cgroups to read.
//
// An APE runs on Linux, macOS and Windows from one binary, and only Linux
// publishes /proc/self/cgroup. Ask the host directly rather than opening a
// path that does not exist on two of the three.
func cgroupHostSupported() bool {
	return __hostos == _HOSTLINUX
}
