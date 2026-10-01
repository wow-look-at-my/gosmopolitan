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

// A struct literal default names fields the caller's package cannot: the
// literal is evaluated as this package, not as the caller.
type Budget struct {
	ints, floats int
	wide         bool
	inner        Limits
}

type Limits struct{ n int }

func Sum(b Budget = Budget{ints: 9, floats: 15, wide: true, inner: Limits{n: 1}}) int {
	sum := b.ints + b.floats + b.inner.n
	if b.wide {
		sum *= 2
	}
	return sum
}

func Zero(b Budget = Budget{}) int { return b.ints + b.floats + b.inner.n }
