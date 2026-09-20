// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"strings"
	"testing"

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

// cgo writes this package's own directory into the Go file it generates, so
// two GOROOTs at different paths must not share one cache entry for it.
// Sharing one is what handed cmd/vet a path holding no file.
func TestOriginKeyCgoGorootSeparatesTrees(t *testing.T) {
	here := packageOriginKey(gorootPackage("/home/user/gosmopolitan/src/crypto/x", true), false, testWorkDir)
	there := packageOriginKey(gorootPackage("/home/runner/work/gosmopolitan/src/crypto/x", true), false, testWorkDir)

	require.NotEqual(t, there, here, "two GOROOTs share one cgo key")
	assert.Contains(t, here, "/home/user/gosmopolitan/src/crypto/x")
}

// Every other GOROOT package has its directory rewritten out of the output,
// so the key leaves GOROOT alone and the two trees share their entries.
func TestOriginKeyPlainGorootSharesTrees(t *testing.T) {
	here := packageOriginKey(gorootPackage("/home/user/gosmopolitan/src/fmt", false), false, testWorkDir)
	there := packageOriginKey(gorootPackage("/home/runner/work/gosmopolitan/src/fmt", false), false, testWorkDir)

	assert.Equal(t, there, here, "GOROOT reached the key")
	assert.Empty(t, here)
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
