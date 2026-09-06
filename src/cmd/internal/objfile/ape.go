// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objfile

import "internal/ape"

// apeDebugFile returns the sidecar to read symbols from for the APE at
// name, or "" when name is not a stripped APE or no sidecar is beside
// it.
//
// A stripped APE has no runtime.pclntab, so every tool over this
// package reports it as an unrecognized or symbol-less object. The
// symbols are one file away, and gdb and delve already read them from
// there, so these tools read them from there too.
func apeDebugFile(name string) string { return ape.Sidecar(name) }
