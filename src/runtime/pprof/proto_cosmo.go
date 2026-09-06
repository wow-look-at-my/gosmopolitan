// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package pprof

import (
	"errors"
	"os"
	"runtime"
	_ "unsafe" // for go:linkname
)

//go:linkname pprof_mainModuleText runtime.pprof_mainModuleText
func pprof_mainModuleText() (uintptr, uintptr)

// readMapping writes this process's mappings to b.pb. A Linux host
// publishes them in /proc; the other two do not, and the runtime's own
// text range is the mapping a profile needs to symbolize.
func (b *profileBuilder) readMapping() {
	if runtime.GOOS == "linux" {
		data, _ := os.ReadFile("/proc/self/maps")
		parseProcSelfMaps(data, b.addMapping)
		if len(b.mem) > 0 {
			return
		}
	}
	start, end, exe, buildID, err := readMainModuleMapping()
	if err != nil {
		b.addMappingEntry(0, 0, 0, "", "", true)
		return
	}
	b.addMapping(start, end, start, exe, buildID)
}

// readMainModuleMapping reports the main module's text range. buildID
// stays empty: no host answers it the same way, and a wrong one names
// the wrong binary.
func readMainModuleMapping() (start, end uint64, exe, buildID string, err error) {
	text, etext := pprof_mainModuleText()
	if text == 0 || etext <= text {
		return 0, 0, "", "", errors.New("runtime reports no text range")
	}
	exe, err = os.Executable()
	if err != nil {
		return 0, 0, "", "", err
	}
	return uint64(text), uint64(etext), exe, "", nil
}
