// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package test

import (
	"internal/testenv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rxProfileSource gives the regexp pass each kind of site. A comment names
// what -d=regexpprecompile must print for its line. No function is called,
// so no inlined copy adds a site.
const rxProfileSource = `package main

import "regexp"

var global = "x+"

func param(pat string) *regexp.Regexp {
	return regexp.MustCompile(pat) // dynamic: the parameter pat
}

func fromGlobal() *regexp.Regexp {
	return regexp.MustCompile(global) // dynamic: the package variable global
}

func first() *regexp.Regexp {
	return regexp.MustCompile(` + "`a+b`" + `) // precompiled
}

func again() *regexp.Regexp {
	return regexp.MustCompile(` + "`a+b`" + `) // shared
}

func posix() *regexp.Regexp {
	return regexp.MustCompilePOSIX(` + "`(c|cd)`" + `) // precompiled
}

func invalid() (*regexp.Regexp, error) {
	return regexp.Compile(` + "`(`" + `) // dynamic: the pattern is invalid
}

func main() {}
`

// rxProfileCompile compiles rxProfileSource with go tool compile and the
// given flags, and returns the compiler's output.
func rxProfileCompile(t *testing.T, dir string, flags ...string) string {
	t.Helper()
	args := append([]string{"tool", "compile", "-p=main", "-importcfg=importcfg", "-o=main.o"}, flags...)
	cmd := testenv.Command(t, testenv.GoToolPath(t), append(args, "main.go")...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", cmd, err, out)
	}
	return string(out)
}

// rxProfileLines returns the line numbers of rxProfileSource whose comment
// starts with mark.
func rxProfileLines(mark string) map[int]string {
	lines := map[int]string{}
	for idx, line := range strings.Split(rxProfileSource, "\n") {
		if _, comment, found := strings.Cut(line, "// "); found && strings.HasPrefix(comment, mark) {
			lines[idx+1] = strings.TrimSpace(strings.TrimPrefix(comment, mark+":"))
		}
	}
	return lines
}

// TestRegexpPrecompileInstrumentation checks that -bench splits the pass
// into its phases with their counts, and that -d=regexpprecompile prints
// each site.
func TestRegexpPrecompileInstrumentation(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(rxProfileSource), 0o666); err != nil {
		t.Fatal(err)
	}
	testenv.WriteImportcfg(t, filepath.Join(dir, "importcfg"), nil, "regexp")

	out := rxProfileCompile(t, dir, "-bench=bench.txt", "-d=regexpprecompile=1")
	bench, err := os.ReadFile(filepath.Join(dir, "bench.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("-bench:\n%s", bench)
	t.Logf("-d=regexpprecompile=1:\n%s", out)

	// Each phase line is a label, a count, ns/op and a percentage, then a
	// pair of columns for each event: the amount and the amount per second.
	phase := regexp.MustCompile(`(?m)^BenchmarkCompile:main:fe:(regexp-precompile:\w+|pre-escape)\s+1\s+(\d+) ns/op\s+\S+ %(.*)$`)
	event := regexp.MustCompile(`(\d+) ([A-Za-z]+)(?:\s|$)`)
	var order []string
	events := map[string]map[string]int64{}
	for _, match := range phase.FindAllStringSubmatch(string(bench), -1) {
		order = append(order, match[1])
		events[match[1]] = map[string]int64{}
		for _, pair := range event.FindAllStringSubmatch(match[3], -1) {
			num, err := strconv.ParseInt(pair[1], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			events[match[1]][pair[2]] = num
		}
	}
	wantOrder := "regexp-precompile:scan regexp-precompile:compile regexp-precompile:emit pre-escape"
	if got := strings.Join(order, " "); got != wantOrder {
		t.Fatalf("-bench phases %q, want %q\n%s", got, wantOrder, bench)
	}
	counts := map[string]map[string]int64{
		"regexp-precompile:scan":    {"calls": 6, "dynamic": 2, "warnings": 2},
		"regexp-precompile:compile": {"patterns": 3},
		"regexp-precompile:emit":    {"sites": 3},
	}
	for name, want := range counts {
		for unit, num := range want {
			if events[name][unit] != num {
				t.Errorf("-bench %s reports %d %s, want %d", name, events[name][unit], unit, num)
			}
		}
	}
	// Each of the two templates holds a Regexp, its prog, and their strings.
	emitted := events["regexp-precompile:emit"]
	if emitted["B"] < 256 || emitted["syms"] < 4 {
		t.Errorf("-bench emit reports %d B in %d syms, want at least 256 B in 4 syms", emitted["B"], emitted["syms"])
	}

	site := regexp.MustCompile(`(?m)^(?:.*/)?main\.go:(\d+):\d+: regexp precompile: regexp\.(\w+)\((.+)\) precompiled: compile (\S+), (\d+) B read-only data(, shared with an earlier site)?$`)
	precompiled := rxProfileLines("precompiled")
	shared := rxProfileLines("shared")
	matches := site.FindAllStringSubmatch(out, -1)
	if len(matches) != len(precompiled)+len(shared) {
		t.Fatalf("-d=regexpprecompile=1 printed %d sites, want %d\n%s", len(matches), len(precompiled)+len(shared), out)
	}
	var total int64
	for _, match := range matches {
		line, _ := strconv.Atoi(match[1])
		_, isFirst := precompiled[line]
		_, isShared := shared[line]
		if !isFirst && !isShared || isShared != (match[6] != "") {
			t.Errorf("unexpected site line: %s", match[0])
		}
		took, err := time.ParseDuration(match[4])
		if err != nil || took <= 0 {
			t.Errorf("site at line %d reports compile time %q", line, match[4])
		}
		size, _ := strconv.ParseInt(match[5], 10, 64)
		if size <= 0 {
			t.Errorf("site at line %d reports %d B", line, size)
		}
		if !isShared {
			total += size
		}
	}
	if total != emitted["B"] {
		t.Errorf("the sites report %d B, and -bench reports %d B", total, emitted["B"])
	}
	if strings.Contains(out, "stays dynamic") {
		t.Errorf("-d=regexpprecompile=1 printed a dynamic site:\n%s", out)
	}

	out = rxProfileCompile(t, dir, "-d=regexpprecompile=2")
	t.Logf("-d=regexpprecompile=2:\n%s", out)
	dynamicLine := regexp.MustCompile(`(?m)^(?:.*/)?main\.go:(\d+):\d+: regexp precompile: regexp\.\w+ stays dynamic: (.+)$`)
	want := rxProfileLines("dynamic")
	have := dynamicLine.FindAllStringSubmatch(out, -1)
	if len(have) != len(want) {
		t.Fatalf("-d=regexpprecompile=2 printed %d dynamic sites, want %d\n%s", len(have), len(want), out)
	}
	for _, match := range have {
		line, _ := strconv.Atoi(match[1])
		if reason, found := want[line]; !found || !strings.Contains(match[2], reason) {
			t.Errorf("dynamic site at line %d: reason %q, want a reason that names %q", line, match[2], want[line])
		}
	}
	if got := len(site.FindAllString(out, -1)); got != len(matches) {
		t.Errorf("-d=regexpprecompile=2 printed %d precompiled sites, want %d", got, len(matches))
	}
}
