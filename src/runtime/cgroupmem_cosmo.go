// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// cgroupHostSupported reports whether this host has cgroups to read.
func cgroupHostSupported() bool {
	return __hostos == _HOSTLINUX
}
