// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"strings"
	"testing"

	"cmd/go/internal/cache"
	"cmd/go/internal/load"
	"cmd/go/internal/modinfo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWorkDir = "/tmp/go-build123"

// gorootPackage returns a GOROOT package rooted at dir, with cgo when the
// caller asks for it.
func gorootPackage(dir string, cgo bool) *load.Package {
	pkg := &load.Package{}
	pkg.Dir = dir
	pkg.Goroot = true
	if cgo {
		pkg.CgoFiles = []string{"seccomp_linux.go"}
	}
	return pkg
}

// A compiled GOROOT package hides its directory, cgo or not, so a tree moved
// to another path stays up to date.
func TestOriginKeyGorootSharesTrees(t *testing.T) {
	for _, cgo := range []bool{false, true} {
		here := packageOriginKey(gorootPackage("/home/user/gosmopolitan/src/crypto/x", cgo), false, testWorkDir)
		there := packageOriginKey(gorootPackage("/home/runner/work/gosmopolitan/src/crypto/x", cgo), false, testWorkDir)

		assert.Equal(t, there, here, "GOROOT reached the key, cgo=%v", cgo)
		assert.Empty(t, here)
	}
}

// objdirAction returns a build action for pkg under one fixed action ID.
func objdirAction(pkg *load.Package) *Action {
	return &Action{Package: pkg, actionID: cache.ActionID{1}}
}

// cgo writes the package directory into the Go file it generates, and vet
// opens that path. GOROOTs must not share those files.
func TestObjdirKeyCgoGorootSeparatesTrees(t *testing.T) {
	here := objdirKey(objdirAction(gorootPackage("/home/user/gosmopolitan/src/crypto/x", true)))
	there := objdirKey(objdirAction(gorootPackage("/home/runner/work/gosmopolitan/src/crypto/x", true)))

	require.NotEqual(t, there, here, "two GOROOTs share one cgo file")
}

// Every other package keeps its files under the action ID itself.
func TestObjdirKeyPlainIsTheActionID(t *testing.T) {
	act := objdirAction(gorootPackage("/home/user/gosmopolitan/src/fmt", false))

	assert.Equal(t, act.actionID, objdirKey(act))
}

// -trimpath takes the directory out of the output, cgo included.
func TestOriginKeyTrimpathDropsTheDirectory(t *testing.T) {
	key := packageOriginKey(gorootPackage("/home/user/gosmopolitan/src/crypto/x", true), true, testWorkDir)

	assert.Empty(t, key, "key names something under -trimpath")
}

// A module built under -trimpath reports its path and version instead.
func TestOriginKeyTrimpathNamesTheModule(t *testing.T) {
	pkg := gorootPackage("/home/user/go/pkg/mod/example.com/dep@v1.2.3", true)
	pkg.Goroot = false
	pkg.Module = &modinfo.ModulePublic{Path: "example.com/dep", Version: "v1.2.3"}

	assert.Equal(t, "module example.com/dep@v1.2.3\n", packageOriginKey(pkg, true, testWorkDir))
}

// A package outside GOROOT keeps naming its directory. One inside the build's
// own work directory names nothing, because that path is rewritten too.
func TestOriginKeyOutsideGoroot(t *testing.T) {
	outside := gorootPackage("/home/user/project/pkg", false)
	outside.Goroot = false
	inside := gorootPackage(testWorkDir+"/b001", false)
	inside.Goroot = false

	assert.Equal(t, "dir /home/user/project/pkg\n", packageOriginKey(outside, false, testWorkDir))
	assert.Empty(t, packageOriginKey(inside, false, testWorkDir), "the work directory reached the key")
}

// A key that names anything ends a line, so it stays separate from the lines
// written around it in the action ID.
func TestOriginKeyLinesTerminate(t *testing.T) {
	keys := []string{
		packageOriginKey(gorootPackage("/src/crypto/x", true), false, testWorkDir),
		packageOriginKey(gorootPackage("/elsewhere/pkg", false), false, testWorkDir),
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		assert.True(t, strings.HasSuffix(key, "\n"), "key %q does not end a line", key)
	}
}
