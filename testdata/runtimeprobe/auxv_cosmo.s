// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

// This file is deliberately empty. auxv_cosmo.go declares getAuxv with
// no body and reaches the runtime's copy through go:linkname, and the
// compiler rejects a bodyless declaration in a package it believes is
// complete. One non-Go file in the package is what tells it otherwise.
