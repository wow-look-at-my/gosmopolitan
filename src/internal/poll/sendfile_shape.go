// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || linux || android

package poll

// sendfileIsLinuxShaped says the platform's sendfile reads from the input file's own position and advances it, so a caller passes no offset.
const sendfileIsLinuxShaped = true
