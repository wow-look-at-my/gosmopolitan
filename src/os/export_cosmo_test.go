// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package os

// SplitPathListSep takes the separator as a parameter, so a host that is not
// NT can still check how an NT PATH parts.
var SplitPathListSep = splitPathListSep
