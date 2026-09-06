// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo || !cosmontdebug

package runtime

// ntBoot is the milestone print with the tag off: nothing, and the
// compiler removes the calls. os_cosmo_nt_boottrace.go has the other
// half.
//
//go:nosplit
func ntBoot(msg string) {}
