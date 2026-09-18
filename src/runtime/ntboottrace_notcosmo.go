// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo || !cosmontdebug

package runtime

// ntBoot is the milestone print with the tag off: nothing, and the
// compiler removes the calls. os_cosmo_nt_boottrace.go has the other
// half.
//
// The name carries no _cosmo component on purpose. cmd/dist reads the
// FILENAME for a GOOS before it reads the build line, so a file called
// os_cosmo_nt_...off.go is cosmo-only to the bootstrap however its
// //go:build line reads, and every other port loses this declaration.
//
//go:nosplit
func ntBoot(msg string) {}

//go:nosplit
func ntBootCode(msg string, v uintptr) {}
