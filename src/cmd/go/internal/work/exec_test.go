// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"cmd/go/internal/base"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"fmt"
	"math/rand"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestEncodeArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		arg, want string
	}{
		{"", `""`},
		{"hello", "hello"},
		{"hello\n", "\"hello\n\""},
		{"hello\\", `"hello\\"`},
		{"hello\nthere", "\"hello\nthere\""},
		{"\\\n", "\"\\\\\n\""},
		{"hello world", `"hello world"`},
		{"hello\tthere", "\"hello\tthere\""},
		{`hello"there`, `"hello\"there"`},
		{"hello$there", `"hello\$there"`},
		{"hello`there", "\"hello\\`there\""},
		{"simple", "simple"},
	}
	for _, test := range tests {
		if got := encodeArg(test.arg); got != test.want {
			t.Errorf("encodeArg(%q) = %q, want %q", test.arg, got, test.want)
		}
	}
}

func TestEncodeDecode(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		"hello",
		"hello\\there",
		"hello\nthere",
		"hello 中国",
		"hello \n中\\国",
		"hello$world",
		"hello`world",
		`hello"world`,
	}
	for _, arg := range tests {
		encoded := encodeArg(arg)
		args := objabi.ParseArgs([]byte(encoded))
		if len(args) != 1 || args[0] != arg {
			t.Errorf("ParseArgs(encodeArg(%q)) = %q (encoded: %q)", arg, args, encoded)
		}
	}
}

func TestEncodeDecodeFuzz(t *testing.T) {
	if testing.Short() {
		t.Skip("fuzz test is slow")
	}
	t.Parallel()

	nRunes := sys.ExecArgLengthLimit + 100
	rBuffer := make([]rune, nRunes)
	buf := bytes.NewBuffer([]byte(string(rBuffer)))

	seed := time.Now().UnixNano()
	t.Logf("rand seed: %v", seed)
	rng := rand.New(rand.NewSource(seed))

	for i := 0; i < 50; i++ {
		// Generate a random string of runes.
		buf.Reset()
		for buf.Len() < sys.ExecArgLengthLimit+1 {
			var r rune
			for {
				r = rune(rng.Intn(utf8.MaxRune + 1))
				if utf8.ValidRune(r) {
					break
				}
			}
			fmt.Fprintf(buf, "%c", r)
		}
		arg := buf.String()

		encoded := encodeArg(arg)
		args := objabi.ParseArgs([]byte(encoded))
		if len(args) != 1 || args[0] != arg {
			t.Errorf("[%d] ParseArgs(encodeArg(%q)) = %q [seed: %v]", i, arg, args, seed)
		}
	}
}

// TestResponseFileTool checks that a tool linked into the go command, which
// runs as "<go> tool <name>", is recognized as that tool and not as the go
// command. A missed tool gets no response file, and the whole command line
// goes to the operating system.
func TestResponseFileTool(tst *testing.T) {
	base.SetSelf("/goroot/bin/go", []string{"compile", "link"})

	cases := []struct {
		args     []string
		prog     string
		toolArgs int
	}{
		{[]string{"/goroot/bin/go", "tool", "link", "-o", "out"}, "link", 3},
		{[]string{"/goroot/bin/go.exe", "tool", "compile", "-p", "main"}, "compile", 3},
		{[]string{"/goroot/pkg/tool/linux_amd64/link", "-o", "out"}, "link", 1},
		{[]string{"/goroot/pkg/tool/windows_amd64/link.exe", "-o", "out"}, "link", 1},
		{[]string{"/goroot/bin/go", "tool", "nosuch", "-o", "out"}, "go", 1},
		{[]string{"/usr/bin/gcc", "-o", "out"}, "gcc", 1},
	}
	for _, one := range cases {
		prog, toolArgs := responseFileTool(one.args)
		if prog != one.prog || toolArgs != one.toolArgs {
			tst.Errorf("responseFileTool(%q) = %q, %d; want %q, %d",
				one.args, prog, toolArgs, one.prog, one.toolArgs)
		}
	}
}

// TestLinkedToolResponseFile checks that the response file written for a
// linked tool keeps the "tool <name>" words that select it.
func TestLinkedToolResponseFile(tst *testing.T) {
	base.SetSelf("/goroot/bin/go", []string{"link"})

	long := strings.Repeat("a", sys.ExecArgLengthLimit+1)
	cmd := &exec.Cmd{
		Path: "/goroot/bin/go",
		Args: []string{"/goroot/bin/go", "tool", "link", "-o", "out", long},
	}
	cleanup := passLongArgsInResponseFiles(cmd)
	defer cleanup()

	want := []string{"/goroot/bin/go", "tool", "link"}
	if len(cmd.Args) != 4 || !slices.Equal(cmd.Args[:3], want) {
		tst.Fatalf("args after rewrite = %q; want %q and one response file", cmd.Args, want)
	}
	if !strings.HasPrefix(cmd.Args[3], "@") {
		tst.Fatalf("last argument %q is not a response file", cmd.Args[3])
	}
}
