// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import _ "unsafe" // for go:linkname

// pprof_mainModuleText reports the address range of the main module's
// text. Each host publishes its own mappings somewhere else - /proc on
// Linux, mach_vm_region on macOS, the module list on NT - and one APE
// meets all three, while the runtime already knows the range on every
// one of them.
//
//go:linkname pprof_mainModuleText
func pprof_mainModuleText() (uintptr, uintptr) {
	md := &firstmoduledata
	return md.text, md.etext
}
