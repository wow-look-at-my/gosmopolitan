// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A "readonly var" is assignable inside its own package. The refusal from
// another package is in test/readonly.dir. Depth: docs/READONLY-VARS.md.

package readonly

readonly var host string = "unknown"

readonly var (
	count int
	names []string
)

func _() {
	host = "x"
	p := &host
	*p = "y"
	count++
	names = append(names, host)
}

// The word is a keyword only where a declaration starts.
var readonly = 1

func _() int {
	readonly := 2
	return readonly
}
