// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package os

import "runtime"

// mkdir(2) and open(2) carry the sticky bit on Linux and drop it on the
// BSDs, and one APE meets a BSD kernel whenever the host is macOS. NT has
// no sticky bit at all. So this is a variable read from the host rather
// than a build-time constant, and Mkdir chmods the bit on afterwards
// wherever the create call will not carry it.
var supportsCreateWithStickyBit = runtime.GOOS == "linux"
