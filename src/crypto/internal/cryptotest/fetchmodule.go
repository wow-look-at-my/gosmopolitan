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
	if !dirExists(gopath) {
		tmp := t.TempDir()
		t.Setenv("GOPATH", tmp)
		if dirExists(gomodcache) {
			// A GOFIPS140 snapshot lives in its own GOMODCACHE. Keep it.
			t.Setenv("GOMODCACHE", gomodcache)
		} else {
			t.Setenv("GOMODCACHE", filepath.Join(tmp, "pkg", "mod"))
			// Allow t.TempDir() to clean up subdirectories.
			t.Setenv("GOFLAGS", os.Getenv("GOFLAGS")+" -modcacherw")
		}
	} else if !dirExists(gomodcache) {
		t.Setenv("GOMODCACHE", t.TempDir())
		t.Setenv("GOFLAGS", os.Getenv("GOFLAGS")+" -modcacherw")
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
