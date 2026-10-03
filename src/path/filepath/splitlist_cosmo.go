// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package filepath

import (
	"runtime"
	"strings"
)

// splitList follows the host, because one APE reads both shapes of PATH.
func splitList(path string) []string {
	if path == "" {
		return []string{}
	}
	if runtime.CosmoHostOS() != "windows" {
		return strings.Split(path, string(ListSeparator))
	}
	return splitListQuoted(path, byte(ListSeparator))
}

// splitListQuoted is path_windows.go's splitList, with the separator passed in
// so a host that does not use it can still test this. An NT entry may be
// quoted, and a quoted entry may hold the separator itself, so a plain split
// cuts inside one. os/exec carries its own copy of this; change that one too.
func splitListQuoted(path string, sep byte) []string {
	// Split, respecting but preserving quotes.
	list := []string{}
	start := 0
	quo := false
	for i := 0; i < len(path); i++ {
		switch c := path[i]; {
		case c == '"':
			quo = !quo
		case c == sep && !quo:
			list = append(list, path[start:i])
			start = i + 1
		}
	}
	list = append(list, path[start:])

	// Remove quotes.
	for i, s := range list {
		list[i] = strings.ReplaceAll(s, `"`, ``)
	}

	return list
}
