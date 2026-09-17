// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cmd/go/internal/cfg"
)

// Each package of a module is generated in its own copy of the fetched module
// and published into the one tree builds read. The second publish must add what
// its generator wrote and remove what it removed, and leave the first package's
// output where it was.
func TestPublishGeneratedKeepsEveryPackage(test *testing.T) {
	base := test.TempDir()
	modroot := filepath.Join(base, "mod")
	root := filepath.Join(base, "generate")
	// Cleanups run in reverse, so this runs before TempDir's removal, which
	// cannot remove a read-only tree.
	test.Cleanup(func() { makeTreeWritable(root) })

	writeFiles(test, modroot, map[string]string{
		"go.mod":        "module example.com/mod\n",
		"one/one.go":    "package one\n",
		"two/two.go":    "package two\n",
		"two/stale.go":  "package two\n",
		"shared/doc.go": "package shared\n",
	})

	for _, step := range []struct {
		pkg   string
		write map[string]string
		drop  string
	}{
		{"one", map[string]string{"one/one.gen.go": "package one\n// one\n"}, ""},
		{"two", map[string]string{"two/two.gen.go": "package two\n// two\n", "two/data/table.txt": "rows\n"}, "two/stale.go"},
	} {
		stage := filepath.Join(base, "stage-"+step.pkg)
		if err := copyTree(modroot, stage); err != nil {
			test.Fatal(err)
		}
		writeFiles(test, stage, step.write)
		if step.drop != "" {
			if err := os.Remove(filepath.Join(stage, step.drop)); err != nil {
				test.Fatal(err)
			}
		}
		if err := publishGenerated(modroot, stage, root); err != nil {
			test.Fatalf("publishing %s: %v", step.pkg, err)
		}
		if _, err := os.Stat(stage); !errors.Is(err, fs.ErrNotExist) {
			test.Errorf("stage for %s survived its publish: %v", step.pkg, err)
		}
	}

	for rel, want := range map[string]string{
		"one/one.gen.go":     "package one\n// one\n",
		"two/two.gen.go":     "package two\n// two\n",
		"two/data/table.txt": "rows\n",
		"shared/doc.go":      "package shared\n",
	} {
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			test.Errorf("%s is missing from the tree: %v", rel, err)
			continue
		}
		if string(got) != want {
			test.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "two/stale.go")); !errors.Is(err, fs.ErrNotExist) {
		test.Errorf("two/stale.go, which the generator removed, is still in the tree: %v", err)
	}

	sealed := readOnlyDirWriteBits(test)
	for _, rel := range []string{".", "one", "two", "two/data"} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			test.Fatal(err)
		}
		if info.Mode()&0o222 != sealed {
			test.Errorf("%s is writable after publishing: %v", rel, info.Mode())
		}
		if sealed != 0 && !info.IsDir() {
			test.Errorf("%s is not a directory after publishing: %v", rel, info.Mode())
		}
	}
}

// readOnlyDirWriteBits answers the write bits a directory reports on this
// platform once os.Chmod has made it read-only. That is none wherever files
// carry permissions. wasip1 has no permission model: its Chmod changes nothing
// and its Stat reports every directory as 0700.
func readOnlyDirWriteBits(test *testing.T) fs.FileMode {
	test.Helper()
	probe := filepath.Join(test.TempDir(), "probe")
	if err := os.Mkdir(probe, 0o777); err != nil {
		test.Fatal(err)
	}
	if err := os.Chmod(probe, 0o555); err != nil {
		test.Fatal(err)
	}
	info, err := os.Stat(probe)
	if err != nil {
		test.Fatal(err)
	}
	if err := os.Chmod(probe, 0o755); err != nil {
		test.Fatal(err)
	}
	return info.Mode() & 0o222
}

func writeFiles(test *testing.T, dir string, files map[string]string) {
	test.Helper()
	for rel, body := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o666); err != nil {
			test.Fatal(err)
		}
	}
}

// A directive names a program, and a consumer's host has the go command and
// whatever PATH carries. klauspost/compress directs stringer at a package and
// ships the file stringer wrote; a host without stringer reported that
// package as failing to generate, on every build, twice. So a directive whose
// program this host cannot start is skipped, and a package with nothing left
// to run is not generated at all.
func TestDirectivesSkipAProgramTheHostCannotStart(test *testing.T) {
	bin := test.TempDir()
	test.Setenv("PATH", bin)
	onPath := filepath.Join(bin, "present")
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		test.Fatal(err)
	}

	for _, row := range []struct {
		name     string
		files    map[string]string
		runnable int
		skipped  []string
		kept     []string
	}{
		{
			name: "the author's tool beside a go run",
			files: map[string]string{
				"a.go": "package a\n//go:generate stringer -type=T\n//go:generate go run ./gen\n",
			},
			runnable: 1,
			skipped:  []string{"//go:generate stringer -type=T"},
			kept:     []string{"//go:generate go run ./gen"},
		},
		{
			name: "nothing this host can start",
			files: map[string]string{
				"a.go": "package a\n//go:generate stringer -type=T\n",
				"b.go": "package a\n\t//go:generate\tmockgen -source=b.go\n",
			},
			runnable: 0,
			skipped:  []string{"//go:generate stringer -type=T", "//go:generate\tmockgen -source=b.go"},
		},
		{
			name: "an alias resolves to what it names",
			files: map[string]string{
				"a.go": "package a\n//go:generate -command gen go run ./gen\n//go:generate -command str stringer\n//go:generate gen x\n//go:generate str -type=T\n",
			},
			runnable: 1,
			skipped:  []string{"//go:generate str -type=T"},
			kept:     []string{"//go:generate gen x"},
		},
		{
			name: "a path, a variable and a name on PATH are left to go generate",
			files: map[string]string{
				"a.go": "package a\n//go:generate ./tool/gen\n//go:generate $GOPATH/bin/gen\n//go:generate present -x\n",
			},
			runnable: 3,
		},
	} {
		test.Run(row.name, func(test *testing.T) {
			dir := test.TempDir()
			writeFiles(test, dir, row.files)

			runnable, skip := directives(dir)

			if runnable != row.runnable {
				test.Errorf("runnable = %d, want %d", runnable, row.runnable)
			}
			if len(row.skipped) == 0 {
				if skip != "" {
					test.Errorf("skip = %q, want none", skip)
				}
				return
			}
			re, err := regexp.Compile(skip)
			if err != nil {
				test.Fatalf("skip %q: %v", skip, err)
			}
			for _, line := range row.skipped {
				if !re.MatchString(line) {
					test.Errorf("skip %q does not match %q", skip, line)
				}
			}
			for _, line := range row.kept {
				if re.MatchString(line) {
					test.Errorf("skip %q matches %q, which this host can run", skip, line)
				}
			}
		})
	}
}

// A host with no sandbox says nothing about the module being built, so the two
// kinds of failure have to stay apart. Reading them as one let a machine
// without bwrap record a permanent verdict against every module it touched,
// and installing bwrap afterwards could not clear it.
func TestSandboxUnavailableSeparatesHostFromModule(test *testing.T) {
	noSandbox := &sandboxUnavailableError{errors.New("bwrap not installed")}

	for _, row := range []struct {
		name string
		err  error
		host bool
	}{
		{"the host has no sandbox", noSandbox, true},
		{"a sandbox failure under a wrapper", fmt.Errorf("run: %w", noSandbox), true},
		{"the generator itself failed", errors.New("exit status 1"), false},
		{"the generator's input was missing", fmt.Errorf("open parser.c: %w", errors.New("no such file")), false},
		{"no failure at all", nil, false},
	} {
		test.Run(row.name, func(test *testing.T) {
			if got := sandboxUnavailable(row.err); got != row.host {
				test.Errorf("sandboxUnavailable(%v) = %v, want %v", row.err, got, row.host)
			}
		})
	}
}

// Each backend has to REPORT a missing program as a host fact. The predicate
// above cannot catch a backend that forgets to say so: it only reads what it is
// handed. darwin forgot, and a mac without sandbox-exec would have written a
// permanent verdict against every module it touched.
func TestEveryBackendReportsAMissingProgramAsAHostFact(test *testing.T) {
	for _, row := range []struct {
		name  string
		build func(string, []string) ([]string, error)
	}{
		{"linux", bwrapArgv},
		{"darwin", seatbeltArgv},
	} {
		test.Run(row.name, func(test *testing.T) {
			test.Setenv("PATH", test.TempDir())

			_, err := row.build(test.TempDir(), []string{"go", "generate", "./..."})
			if err == nil {
				test.Fatal("a backend found its program on an empty PATH")
			}
			if !sandboxUnavailable(err) {
				test.Errorf("%v reads as a failure of the module, not of the host", err)
			}
		})
	}
}

// The message has to name the missing program, because the reader's next move
// is to install it.
func TestSandboxUnavailableKeepsItsMessage(test *testing.T) {
	inner := errors.New(`exec: "bwrap": executable file not found in $PATH`)
	wrapped := &sandboxUnavailableError{inner}

	if got := wrapped.Error(); got != inner.Error() {
		test.Errorf("Error() = %q, want %q", got, inner.Error())
	}
	if !errors.Is(wrapped, inner) {
		test.Error("the cause did not survive wrapping")
	}
}

// A generator's verdict is written once and replayed by every later build, so
// what rides the error is all a reader ever sees. "exit status 1" on its own
// sent a session hunting a runtime panic whose cause was printed hours before,
// in a build nobody still had the log of.
func TestTailWriterKeepsTheEndAndBoundsWhatItKeeps(test *testing.T) {
	for _, row := range []struct {
		name   string
		limit  int
		writes []string
		want   string
	}{
		{"short output survives whole", 16, []string{"open parser.c"}, "open parser.c"},
		{"the end wins over the start", 8, []string{"0123456789abcdef"}, "89abcdef"},
		{"writes across calls still tail", 6, []string{"aaaa", "bbbb", "cccc"}, "bbcccc"},
		{"nothing written reads empty", 8, nil, ""},
	} {
		test.Run(row.name, func(test *testing.T) {
			sink := &tailWriter{limit: row.limit}
			for _, payload := range row.writes {
				num, err := sink.Write([]byte(payload))
				if err != nil {
					test.Fatalf("Write(%q) failed: %v", payload, err)
				}
				// A short count makes io.MultiWriter report a write error and
				// take the generator's output away entirely.
				if num != len(payload) {
					test.Fatalf("Write(%q) = %d, want %d", payload, num, len(payload))
				}
			}
			if got := sink.String(); got != row.want {
				test.Errorf("String() = %q, want %q", got, row.want)
			}
			if got := len(sink.String()); got > row.limit {
				test.Errorf("kept %d bytes, over the %d-byte limit", got, row.limit)
			}
		})
	}
}

// A generated tree belongs to the go command that wrote it. Its root names
// that go command with a single path element below the generate cache, so a
// binary linking another go command reads a tree of its own.
func TestGeneratedTreeBelongsToTheGoCommand(test *testing.T) {
	cache := test.TempDir()
	was := cfg.GOMODCACHE
	cfg.GOMODCACHE = cache
	test.Cleanup(func() { cfg.GOMODCACHE = was })

	key := goCommandKey()
	if key == "" {
		test.Fatal("goCommandKey() is empty, so every go command would share one tree")
	}
	if strings.ContainsRune(key, filepath.Separator) {
		test.Fatalf("goCommandKey() = %q spans directories", key)
	}
	modrel := filepath.Join("example.com", "mod@v1.0.0")
	want := filepath.Join(cache, "cache", "generate", key, modrel)
	if got := generateRoot(modrel); got != want {
		test.Errorf("generateRoot(%q) = %q, want %q", modrel, got, want)
	}
}
