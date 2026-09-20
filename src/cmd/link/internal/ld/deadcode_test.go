// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"internal/testenv"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestDeadcode(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	t.Parallel()

	tmpdir := t.TempDir()

	// Every assertion below reads the linker's own -dumpdep output. cmd/go
	// replays a cached link's diagnostics as best effort only, so a replay
	// that fails leaves the build green and silent and fails every pattern
	// here. The linker ignores an -X for a symbol it does not have, so this
	// nonce only makes the link action new, and the linker runs for real.
	nonce := "-X=cmd/link/internal/ld.deadcodeTestNonce=" + strconv.FormatInt(time.Now().UnixNano(), 36)

	tests := []struct {
		src      string
		pos, neg []string // positive and negative patterns
	}{
		{"reflectcall", nil, []string{"main.T.M"}},
		{"typedesc", nil, []string{"type:main.T"}},
		{"ifacemethod", nil, []string{"main.T.M"}},
		{"ifacemethod2", []string{"main.T.M"}, nil},
		{"ifacemethod3", []string{"main.S.M"}, nil},
		{"ifacemethod4", nil, []string{"main.T.M"}},
		{"ifacemethod5", []string{"main.S.M"}, nil},
		{"ifacemethod6", []string{"main.S.M"}, []string{"main.S.N"}},
		{"structof_funcof", []string{"main.S.M"}, []string{"main.S.N"}},
		{"globalmap", []string{"main.small", "main.effect"},
			[]string{"main.large"}},
	}
	for _, test := range tests {
		t.Run(test.src, func(t *testing.T) {
			t.Parallel()
			src := filepath.Join("testdata", "deadcode", test.src+".go")
			exe := filepath.Join(tmpdir, test.src+".exe")
			cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-ldflags=-dumpdep "+nonce, "-o", exe, src)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %v:\n%s", cmd.Args, err, out)
			}
			if len(bytes.TrimSpace(out)) == 0 {
				t.Fatalf("%v: the linker dumped no dependencies, so the build reused a cached link", cmd.Args)
			}
			for _, pos := range test.pos {
				if !bytes.Contains(out, []byte(pos+"\n")) {
					t.Errorf("%s should be reachable. Output:\n%s", pos, out)
				}
			}
			for _, neg := range test.neg {
				if bytes.Contains(out, []byte(neg+"\n")) {
					t.Errorf("%s should not be reachable. Output:\n%s", neg, out)
				}
			}
		})
	}
}
