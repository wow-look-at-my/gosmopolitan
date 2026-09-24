// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

// Host is set once, here, and read everywhere else.
readonly var Host string = "unknown"

readonly var (
	Count int
	Names []string
)

// The declaring package assigns, takes the address, and increments freely.
func SetHost(s string) {
	Host = s
	p := &Host
	*p = s
	Count++
	Names = append(Names, s)
}

// A local named readonly is an ordinary identifier.
func readonlyName() int {
	readonly := 1
	return readonly
}
