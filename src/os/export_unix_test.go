// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || (js && wasm) || wasip1

package os

var SplitPath = splitPath

// NTTempDir is tempDir's NT branch, which a cosmo binary reaches when it boots
// on a windows host. Exported so the test runs on the host it is already on.
var NTTempDir = ntTempDir
