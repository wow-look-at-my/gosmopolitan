// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package objfile

import "internal/ape"

// apeDebugFile returns the sidecar to read symbols from for the APE at name, or "" when name is not a stripped APE.
func apeDebugFile(name string) string { return ape.Sidecar(name) }
