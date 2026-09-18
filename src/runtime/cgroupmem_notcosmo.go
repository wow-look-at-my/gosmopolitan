// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && !cosmo

package runtime

// cgroupHostSupported reports whether this host has cgroups to read. A Linux
// binary always runs on Linux.
func cgroupHostSupported() bool { return true }
