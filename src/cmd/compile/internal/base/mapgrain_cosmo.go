// Copyright 2026 The Go Authors. All rights reserved. Use of this source code
// is governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package base

import (
	"os"
	"runtime"
)

// ntAllocationGranularity is what NT rounds a mapping's file offset down to.
const ntAllocationGranularity = 64 << 10

// mapOffsetGrain answers what a mapping's file offset must be a multiple of
// on the host this APE booted on. NT maps at its allocation granularity,
// which is wider than a page, and refuses an offset aligned to a page inside
// it. An archive sits where it sits in the blob, so nearly every import asks
// for such an offset.
func mapOffsetGrain() int64 {
	if runtime.CosmoHostOS() == "windows" {
		return ntAllocationGranularity
	}
	return int64(os.Getpagesize())
}
