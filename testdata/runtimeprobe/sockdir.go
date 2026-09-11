// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "os"

// sunPathMax is the smallest sun_path any host here offers. XNU holds
// 104 bytes, Linux 108. A bind over the limit answers EINVAL.
const sunPathMax = 104

// shortSockDir makes a directory for a unix-socket path. A sandbox
// hands out a temp directory whose name alone can spend most of
// sun_path, so this prefers /tmp, which every unix host has and which
// leaves room for the socket name. A host without it - NT - falls back
// to the ordinary temp directory.
func shortSockDir(prefix string) (string, error) {
	if dir, err := os.MkdirTemp("/tmp", prefix); err == nil {
		return dir, nil
	}
	return os.MkdirTemp("", prefix)
}

// isUnnamedSockName reports whether a unix socket's own name says it is
// unnamed. getsockname answers such a socket with the family alone, and
// Go reads the zero byte left in the buffer as an abstract address, so
// it renders "@" - upstream Go on Linux does the same, and so does the
// NT stack. A bound socket carries a path, which is what this refuses.
func isUnnamedSockName(name string) bool { return name == "" || name == "@" }
