// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"slices"

	"cmd/internal/objabi"
	"cmd/internal/telemetry/counter"
	"cmd/vet/internal/serialtest"

	"golang.org/x/tools/go/analysis/suite/vet"
	"golang.org/x/tools/go/analysis/unitchecker"
)

func main() {
	// Keep consistent with cmd/fix/main.go!
	counter.Open()
	objabi.AddVersionFlag()
	counter.Inc("vet/invocations")

	// serialtest is this fork's own, and the vendored suite cannot
	// carry it. Tests are parallel by default here, so it is the check
	// that says which of them may not be.
	suite := append(slices.Clone(vet.Suite), serialtest.Analyzer)
	unitchecker.Main(suite...) // (never returns)
}
