// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package b

import "./a"

// Reads are ordinary.
var host = a.Host
var n = a.Count + len(a.Names)

func read() string { return a.Host }

func write() {
	a.Host = "x"          // ERROR "cannot assign to a.Host: it is readonly outside package a"
	a.Host += "x"         // ERROR "cannot assign to a.Host: it is readonly outside package a"
	a.Count++             // ERROR "cannot assign to a.Count: it is readonly outside package a"
	a.Names[0] = "x"      // ERROR "cannot assign to a.Names\[0\]"
	_ = &a.Host           // ERROR "cannot take address of a.Host"
	a.Host, a.Count = "", 0 // ERROR "cannot assign to a.Host: it is readonly outside package a" "cannot assign to a.Count: it is readonly outside package a"
	a.SetHost("ok")
}
