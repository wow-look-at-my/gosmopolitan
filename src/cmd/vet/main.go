// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"cmd/internal/objabi"
	"cmd/internal/telemetry/counter"
	"cmd/vet/internal/testglobals"

	"golang.org/x/tools/go/analysis/suite/vet"
	"golang.org/x/tools/go/analysis/unitchecker"
)

func main() {
	// Keep consistent with cmd/fix/main.go!
	counter.Open()
	objabi.AddVersionFlag()
	counter.Inc("vet/invocations")

	// testglobals is this fork's own: top level tests run in parallel here, so
	// a package variable a test writes is one every other test can see.
	unitchecker.Main(append(vet.Suite, testglobals.Analyzer)...) // (never returns)
}
