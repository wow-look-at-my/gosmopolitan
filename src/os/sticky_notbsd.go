// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !aix && !cosmo && !darwin && !dragonfly && !freebsd && !js && !netbsd && !openbsd && !solaris && !wasip1

package os

const supportsCreateWithStickyBit = true
