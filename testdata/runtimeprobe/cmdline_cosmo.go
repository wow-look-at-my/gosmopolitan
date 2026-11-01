// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"errors"
	"os"
	"runtime"
)

// darwinProcArgs reads this process's argv the way ps reads it on macOS.
func darwinProcArgs() ([]byte, error) {
	const ctlKern, kernProcArgs2 = 1, 49
	mib := []uint32{ctlKern, kernProcArgs2, uint32(os.Getpid())}
	size, done := runtime.CosmoDarwinSysctl(mib, nil)
	if !done || size == 0 {
		return nil, errors.New("the host reports no size")
	}
	buf := make([]byte, size)
	size, done = runtime.CosmoDarwinSysctl(mib, buf)
	if !done {
		return nil, errors.New("the read failed")
	}
	return buf[:size], nil
}
