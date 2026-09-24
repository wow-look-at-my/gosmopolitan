// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package exec

// lookupCriticalEnv answers the name NT actually spells. A cosmo
// binary's own environment map is the unix one, which matches exactly,
// and the NT block spells the variable "SystemRoot" rather than
// "SYSTEMROOT". An exact lookup comes back empty, and a child handed
// SYSTEMROOT= cannot load the Winsock provider catalogue: every socket
// it opens fails with WSAEPROVIDERFAILEDINIT.
func lookupCriticalEnv(name string) (string, bool) { return ntLookupEnv(name) }
