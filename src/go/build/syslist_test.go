// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"testing"
)

// The port these files are selected for, not the host running the test.
// A cosmo binary runs on three hosts, and goodOSArchFile answers about
// Default, which is the port.
var (
	thisOS    = Default.GOOS
	thisArch  = Default.GOARCH
	otherOS   = anotherOS()
	otherArch = anotherArch()
)

func anotherOS() string {
	if thisOS != "darwin" && thisOS != "ios" {
		return "darwin"
	}
	return "linux"
}

func anotherArch() string {
	if thisArch != "amd64" {
		return "amd64"
	}
	return "386"
}

type GoodFileTest struct {
	name   string
	result bool
}

var tests = []GoodFileTest{
	{"file.go", true},
	{"file.c", true},
	{"file_foo.go", true},
	{"file_" + thisArch + ".go", true},
	{"file_" + otherArch + ".go", false},
	{"file_" + thisOS + ".go", true},
	{"file_" + otherOS + ".go", false},
	{"file_" + thisOS + "_" + thisArch + ".go", true},
	{"file_" + otherOS + "_" + thisArch + ".go", false},
	{"file_" + thisOS + "_" + otherArch + ".go", false},
	{"file_" + otherOS + "_" + otherArch + ".go", false},
	{"file_foo_" + thisArch + ".go", true},
	{"file_foo_" + otherArch + ".go", false},
	{"file_" + thisOS + ".c", true},
	{"file_" + otherOS + ".c", false},
}

func TestGoodOSArch(t *testing.T) {
	for _, test := range tests {
		if Default.goodOSArchFile(test.name, make(map[string]bool)) != test.result {
			t.Fatalf("goodOSArchFile(%q) != %v", test.name, test.result)
		}
	}
}
