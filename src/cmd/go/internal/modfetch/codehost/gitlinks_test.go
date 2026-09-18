// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codehost

import (
	"archive/zip"
	"bytes"
	"io"
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

// TestGitlinksPathSurvivesTheStrip walks the record through what
// (*codeRepo).Zip does to every archive entry: cut the archive prefix, then cut
// the module's own directory. An entry that fails either cut is discarded, so
// the record has to land under both.
func TestGitlinksPathSurvivesTheStrip(t *testing.T) {
	for _, dir := range []string{"", "sub", "sub/deeper"} {
		name, found := strings.CutPrefix(gitlinksPath(dir), archivePrefix)
		if !found {
			t.Fatalf("gitlinksPath(%q) = %q, which is outside the archive prefix", dir, gitlinksPath(dir))
		}
		under := ""
		if dir != "" {
			under = dir + "/"
		}
		name, found = strings.CutPrefix(name, under)
		if !found {
			t.Fatalf("gitlinksPath(%q) sits outside the module directory %q", dir, under)
		}
		if name != gitlinksFile {
			t.Errorf("gitlinksPath(%q) reaches the module zip as %q, want %q", dir, name, gitlinksFile)
		}
	}
}

func TestAppendZipFile(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, err := writer.Create(archivePrefix + "go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("module example.com/mod\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	body := []byte("2222222222222222222222222222222222222222 vendor/dep\n")
	grown, err := appendZipFile(buf.Bytes(), archivePrefix+gitlinksFile, body)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := zip.NewReader(bytes.NewReader(grown), int64(len(grown)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		archivePrefix + "go.mod":     "module example.com/mod\n",
		archivePrefix + gitlinksFile: string(body),
	}
	if len(reader.File) != len(want) {
		t.Fatalf("zip holds %d entries, want %d", len(reader.File), len(want))
	}
	for _, file := range reader.File {
		if !file.Mode().IsRegular() {
			t.Errorf("%s is not a regular file; a module zip drops anything else", file.Name)
		}
		open, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(open)
		open.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want[file.Name] {
			t.Errorf("%s = %q, want %q", file.Name, data, want[file.Name])
		}
	}
}
