// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo && !darwin

package runtime

// sysmonSleep sleeps for usec microseconds between sysmon's ticks.
func sysmonSleep(usec uint32) {
	usleep(usec)
}
