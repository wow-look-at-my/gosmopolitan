// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package poll

import _ "unsafe" // for go:linkname

// runtime_cancelIO ends the transfers other threads have blocked on fd.
func runtime_cancelIO(fd uintptr)
