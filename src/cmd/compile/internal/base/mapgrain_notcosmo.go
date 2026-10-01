// Copyright 2026 The Go Authors. All rights reserved. Use of this source code
// is governed by a BSD-style license that can be found in the LICENSE file.

//go:build unix && !cosmo

package base

import "os"

// mapOffsetGrain answers what a mapping's file offset must be a multiple of.
// A port that builds for its own kernel takes the page size.
func mapOffsetGrain() int64 {
	return int64(os.Getpagesize())
}
