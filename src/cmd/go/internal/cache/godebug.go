// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"internal/godebug"

	"github.com/wow-look-at-my/go-s3-server/cacheclient/cachedisk"
)

// The cache directory lives in a module outside this tree, which reads GODEBUG
// out of the environment. The registry these three settings belong to is here,
// and a setting read through it feeds a runtime/metrics counter. So the go
// command reads them and hands the answers down.
var (
	gocacheverify = godebug.New("gocacheverify")
	gocachehash   = godebug.New("gocachehash")
	gocachetest   = godebug.New("#gocachetest")
)

func init() {
	verify := gocacheverify.Value() == "1"
	if verify {
		gocacheverify.IncNonDefault()
	}
	hash := gocachehash.Value() == "1"
	if hash {
		gocachehash.IncNonDefault()
	}
	test := gocachetest.Value() == "1"
	cachedisk.SetSwitches(verify, hash, test)
}
