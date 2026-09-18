// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"runtime/debug"
)

func init() {
	register("PrintMemoryLimit", PrintMemoryLimit)
}

func PrintMemoryLimit() {
	println(debug.SetMemoryLimit(-1))
}
