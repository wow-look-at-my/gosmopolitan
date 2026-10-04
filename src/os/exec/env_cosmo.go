// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package exec

// lookupCriticalEnv answers the name NT spells.
func lookupCriticalEnv(name string) (string, bool) { return ntLookupEnv(name) }
