// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package os

import "runtime"

// mkdir(2) and open(2) carry the sticky bit on Linux and drop it on the BSDs.
var supportsCreateWithStickyBit = runtime.GOOS == "linux"
