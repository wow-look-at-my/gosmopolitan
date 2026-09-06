// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

// Repeat covers the three constant kinds a default may take.
func Repeat(s string = "x", n int = 2, loud bool = true) string {
	out := ""
	for range n {
		out += s
	}
	if loud {
		out += "!"
	}
	return out
}

// Method reads its default the same way a function does.
type T struct{}

func (T) Scale(by int = 3) int { return by * by }
