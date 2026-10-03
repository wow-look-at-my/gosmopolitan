// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (unix && !cosmo) || (js && wasm) || wasip1

package filepath

import "strings"

func splitList(path string) []string {
	if path == "" {
		return []string{}
	}
	return strings.Split(path, string(ListSeparator))
}
