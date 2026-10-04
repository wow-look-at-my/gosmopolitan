// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cryptotest

import (
	"bytes"
	"encoding/json"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// FetchModule fetches the module at the given version and returns the directory
// containing its source tree. It skips the test if fetching modules is not
// possible in this environment.
func FetchModule(t *testing.T, module, version string) string {
	testenv.MustHaveExternalNetwork(t)

	// The go command keeps the checksum database's tree head under GOPATH/pkg/sumdb, and run.bash sets GOPATH=/nonexist-gopath.
	out, err := testenv.CleanCmdEnv(testenv.Command(t, testenv.GoToolPath(t), "env", "GOPATH", "GOMODCACHE")).Output()
	if err != nil {
		t.Errorf("%s env GOPATH GOMODCACHE: %v\n%s", testenv.GoToolPath(t), err, out)
		if ee, ok := err.(*exec.ExitError); ok {
			t.Logf("%s", ee.Stderr)
		}
		t.FailNow()
	}
	gopath, gomodcache, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	gomodcache = strings.TrimSpace(gomodcache)
	// Every test that fetches shares one module cache. A cache per test
	// downloads the same module once per call.
	if !dirExists(gomodcache) {
		gomodcache = sharedModCache()
		t.Setenv("GOMODCACHE", gomodcache)
	}
	if !dirExists(gopath) {
		// GOMODCACHE defaults to a path under GOPATH, so pin it first.
		t.Setenv("GOMODCACHE", gomodcache)
		t.Setenv("GOPATH", t.TempDir())
	}

	t.Logf("fetching %s@%s\n", module, version)

	cmd := testenv.Command(t, testenv.GoToolPath(t), "mod", "download", "-json", module+"@"+version)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to download %s@%s: %s\nstdout:\n%s\nstderr:\n%s\n", module, version, err, output, stderr.Bytes())
	}
	if stderr.Len() > 0 {
		t.Logf("go mod download stderr:\n%s", stderr.Bytes())
	}
	var j struct {
		Dir string
	}
	if err := json.Unmarshal(output, &j); err != nil {
		t.Fatalf("failed to parse 'go mod download': %s\nstdout:\n%s\nstderr:\n%s\n", err, output, stderr.Bytes())
	}

	return j.Dir
}

// sharedModCache is the module cache of every test process on this machine
// that has no GOMODCACHE. The go command locks the cache, so concurrent
// processes can share it.
func sharedModCache() string {
	return filepath.Join(os.TempDir(), "go-cryptotest-modcache-"+strconv.Itoa(os.Getuid()))
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
