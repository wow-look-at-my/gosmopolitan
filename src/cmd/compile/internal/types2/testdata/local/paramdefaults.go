// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Optional parameters: a named ordinary parameter may carry a constant
// default, and a call may omit a suffix of such parameters. Depth:
// docs/OPTIONAL-PARAMS.md.

package paramdefaults

func read(root string = ".") string { return root }

func between(lo int = 0, hi int = 10) int { return hi - lo }

func flagged(on bool = true) bool { return on }

func required(a int, b string = "x") string { return b }

// An untyped default converts to the parameter type, as an argument would.
func scaled(f float64, n int = 2) float64 { return f * float64(n) }

func _() {
	_ = read()
	_ = read("sub")
	_ = between()
	_ = between(1)
	_ = between(1, 2)
	_ = flagged()
	_ = flagged(false)
	_ = required(1)
	_ = required(1, "y")
	_ = scaled(1)
	_ = scaled(1, 3)
}

// A default rides the signature, so a value of the function type takes it too.
func _() {
	f := read
	_ = f()
}
