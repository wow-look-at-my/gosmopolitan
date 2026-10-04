// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// highPrecisionTicks answers a counter fine enough to measure tens of nanoseconds.
func highPrecisionTicks() (int64, bool) { return ntHighPrecisionTicks() }
