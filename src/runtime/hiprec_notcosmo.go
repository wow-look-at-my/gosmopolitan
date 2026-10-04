// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo

package runtime

// highPrecisionTicks has no host-specific counter to offer here.
func highPrecisionTicks() (int64, bool) { return 0, false }
