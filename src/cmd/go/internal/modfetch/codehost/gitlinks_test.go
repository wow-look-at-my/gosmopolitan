// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package codehost

import (
	"strings"
	"testing"
)

// lsTree builds `git ls-tree -r -z` output from lines shaped "<meta>\t<path>".
func lsTree(entries ...string) []byte {
	return []byte(strings.Join(entries, "\x00") + "\x00")
}

func TestGitlinkLines(t *testing.T) {
	const (
		blob = "100644 blob 1111111111111111111111111111111111111111"
		link = "160000 commit 2222222222222222222222222222222222222222"
		peer = "160000 commit 3333333333333333333333333333333333333333"
	)

	cases := []struct {
		name string
		out  []byte
		dir  string
		want string
	}{
		{
			name: "a repository with no submodule records nothing",
			out:  lsTree(blob+"\tgo.mod", blob+"\tmain.go"),
			want: "",
		},
		{
			name: "a module at the repository root keeps the tree's paths",
			out:  lsTree(blob+"\tgo.mod", link+"\tvendor/dep"),
			want: "2222222222222222222222222222222222222222 vendor/dep\n",
		},
		{
			name: "a module in a subdirectory records paths relative to itself",
			out:  lsTree(blob+"\tsub/go.mod", link+"\tsub/vendor/dep"),
			dir:  "sub",
			want: "2222222222222222222222222222222222222222 vendor/dep\n",
		},
		{
			name: "a gitlink outside the module's directory belongs to another module",
			out:  lsTree(link+"\tsub/vendor/dep", peer+"\tother/dep"),
			dir:  "sub",
			want: "2222222222222222222222222222222222222222 vendor/dep\n",
		},
		{
			name: "every submodule of the module is recorded",
			out:  lsTree(link+"\tvendor/dep", peer+"\ttestdata/helper"),
			want: "2222222222222222222222222222222222222222 vendor/dep\n" +
				"3333333333333333333333333333333333333333 testdata/helper\n",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := string(gitlinkLines(test.out, test.dir))
			if got != test.want {
				t.Errorf("gitlinkLines(..., %q) = %q, want %q", test.dir, got, test.want)
			}
		})
	}
}
