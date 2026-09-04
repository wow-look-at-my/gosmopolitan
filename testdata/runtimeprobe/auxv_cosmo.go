// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	_ "unsafe" // for linkname
)

// getAuxv reads the runtime's auxiliary vector the way an outside package
// does. golang.org/x/sys/cpu declares exactly this linkname.
//
//go:linkname getAuxv runtime.getAuxv
func getAuxv() []uintptr
