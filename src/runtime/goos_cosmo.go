// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import "internal/goarch"

// GOOS is the running program's operating system target:
// one of darwin, freebsd, linux, and so on.
// To view possible combinations of GOOS and GOARCH, run "go tool dist list".
//
// A variable here rather than a constant. One APE boots on Linux, macOS
// and Windows, so the answer is the HOST, which the entry stub records
// before any Go code runs. Everything that switches on GOOS to match
// platform semantics - os.Root's trailing-slash rules, path handling,
// os/exec - then gets the kernel it is actually talking to.
//
// setGOOS runs in osinit, ahead of every package init, so no Go code can
// observe the placeholder.
var GOOS string = "cosmo"

func setGOOS() {
	if s := CosmoHostOS(); s != "unknown" {
		GOOS = s
	}
}

// GOARCH is the running program's architecture target:
// one of 386, amd64, arm, s390x, and so on.
//
// A variable for the same reason as GOOS. The APE picks the payload whose
// architecture matches the machine, so this equals the compiled one on
// every host that boots today. It stops being equal the moment a payload
// runs under emulation, and then the honest answer is the machine.
var GOARCH string = goarch.GOARCH

func setGOARCH() {
	if s := cosmoHostArch(); s != "" {
		GOARCH = s
	}
}
