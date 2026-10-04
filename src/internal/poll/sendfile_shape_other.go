// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !(cosmo || linux || android)

package poll

// sendfileIsLinuxShaped is false on a platform whose sendfile takes the offset as an argument.
const sendfileIsLinuxShaped = false
