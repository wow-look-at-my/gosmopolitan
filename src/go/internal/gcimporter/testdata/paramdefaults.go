// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package paramdefaults

func Repeat(s string = "x", n int = 2, loud bool = true) string { return s }

func Plain(s string) string { return s }

type Budget struct {
	ints, floats int
	Wide         bool
}

func WithBudget(b Budget = Budget{ints: 9, Wide: true}) int { return b.ints }
