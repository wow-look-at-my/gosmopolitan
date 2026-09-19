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
)

const workDir = "/tmp/go-build123"

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

// A cgo package writes its own directory into the Go file cgo generates, so
// two GOROOTs must not share one cache entry for it.
func TestOriginKeyCgoGorootSeparatesTrees(t *testing.T) {
	here := packageOriginKey(gorootPackage("/home/user/gosmopolitan/src/crypto/x", true), false, workDir)
	there := packageOriginKey(gorootPackage("/home/runner/work/gosmopolitan/src/crypto/x", true), false, workDir)

	assert.NotEqual(t, here, there, "two GOROOTs share a cgo key")
	assert.Contains(t, here, "/home/user/gosmopolitan/src/crypto/x")
}

// Every other GOROOT package has its directory rewritten out of the output,
// so the key leaves GOROOT alone and the trees share their entries.
func TestOriginKeyPlainGorootSharesTrees(t *testing.T) {
	here := packageOriginKey(gorootPackage("/home/user/gosmopolitan/src/fmt", false), false, workDir)
	there := packageOriginKey(gorootPackage("/home/runner/work/gosmopolitan/src/fmt", false), false, workDir)

	assert.Equal(t, here, there)
	assert.Empty(t, here)
}

// -trimpath takes the directory out of the output, cgo included.
func TestOriginKeyTrimpathDropsTheDirectory(t *testing.T) {
	key := packageOriginKey(gorootPackage("/home/user/gosmopolitan/src/crypto/x", true), true, workDir)

	assert.Empty(t, key)
}

// A module built under -trimpath reports its path and version instead.
func TestOriginKeyTrimpathNamesTheModule(t *testing.T) {
	pkg := gorootPackage("/home/user/go/pkg/mod/example.com/dep@v1.2.3", true)
	pkg.Goroot = false
	pkg.Module = &modinfo.ModulePublic{Path: "example.com/dep", Version: "v1.2.3"}

	key := packageOriginKey(pkg, true, workDir)

	assert.Equal(t, "module example.com/dep@v1.2.3\n", key)
}

// A package outside GOROOT keeps naming its directory, and one inside the
// build's own work directory names nothing.
func TestOriginKeyOutsideGoroot(t *testing.T) {
	outside := gorootPackage("/home/user/project/pkg", false)
	outside.Goroot = false
	inside := gorootPackage(workDir+"/b001", false)
	inside.Goroot = false

	assert.Equal(t, "dir /home/user/project/pkg\n", packageOriginKey(outside, false, workDir))
	assert.Empty(t, packageOriginKey(inside, false, workDir))
}

// The key ends in a newline whenever it names anything, so the lines around
// it in the action ID stay separate.
func TestOriginKeyLinesTerminate(t *testing.T) {
	for _, key := range []string{
		packageOriginKey(gorootPackage("/src/crypto/x", true), false, workDir),
		packageOriginKey(gorootPackage("/elsewhere/pkg", false), false, workDir),
	} {
		if key == "" {
			continue
		}
		assert.True(t, strings.HasSuffix(key, "\n"), "key %q does not end a line", key)
	}
}
