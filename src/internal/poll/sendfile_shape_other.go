// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(cosmo || linux || android)

package poll

// sendfileIsLinuxShaped is false on a platform whose sendfile takes the
// offset as an argument and leaves the file's own position alone. See
// the file next to this one.
const sendfileIsLinuxShaped = false
