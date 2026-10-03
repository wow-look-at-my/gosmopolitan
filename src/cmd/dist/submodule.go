// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"os/exec"
)

// vendorWitness is a file that only a checked-out src/cmd/vendor submodule
// provides, named relative to src.
const vendorWitness = "cmd/vendor/github.com/wow-look-at-my/go-s3-server/go.mod"

// checkVendorSubmodules stops a build whose src/cmd/vendor submodules are not
// checked out. cmd/go requires packages that live there, so a clone made
// without them fails much later with "no required module provides package",
// which reads as a missing dependency and sends the reader to go get.
func checkVendorSubmodules() {
	if isfile(pathf("%s/src/%s", goroot, vendorWitness)) {
		return
	}
	fatalf("src/cmd/vendor submodules are not checked out.\n"+
		"Run: git submodule update --init --recursive\n")
}

// trackSubmoduleBranches moves every org submodule onto the head of the branch
// it follows, so a build reads the branch a change is on rather than a commit
// somebody wrote down once. submodulebranch.bash holds how that is done, and
// this is the whole of when it runs during a build.
//
// GOSUBMODULEBRANCH=off leaves the checkout alone. So does a host with no bash
// to run the script with, which is said rather than passed over, because the
// build that follows then compiles the commit each gitlink names.
func trackSubmoduleBranches() {
	if os.Getenv("GOSUBMODULEBRANCH") == "off" {
		return
	}
	script := pathf("%s/src/submodulebranch.bash", goroot)
	if !isfile(script) {
		return
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		errprintf("submodulebranch: every submodule stays at its gitlink: %v\n", err)
		return
	}
	run(pathf("%s/src", goroot), ShowOutput, bash, script)
}
