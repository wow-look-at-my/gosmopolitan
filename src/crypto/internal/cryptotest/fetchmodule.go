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

	// The module cache and the checksum database's tree head live under
	// GOPATH. A test never makes a private one: that downloads the module
	// again for every call.
	out, err := testenv.CleanCmdEnv(testenv.Command(t, testenv.GoToolPath(t), "env", "GOPATH")).Output()
	if err != nil {
		t.Errorf("%s env GOPATH: %v\n%s", testenv.GoToolPath(t), err, out)
		if ee, ok := err.(*exec.ExitError); ok {
			t.Logf("%s", ee.Stderr)
		}
		t.FailNow()
	}
	gopath := strings.TrimSpace(string(out))
	if list := filepath.SplitList(gopath); len(list) == 0 || !dirExists(list[0]) {
		t.Fatalf("GOPATH %q is not a directory. run.bash makes one under GOROOT/pkg.", gopath)
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
