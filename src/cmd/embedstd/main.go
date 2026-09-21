// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Embedstd writes the blob a go binary carries its standard library in:
// the compiled archive of every standard package for cosmo/amd64 and
// cosmo/arm64, the assembly headers under pkg/include, and a manifest per
// target naming each package, its imports and its build ID. The linker's
// -apeappend flag puts the blob past an APE's load span, and
// internal/cosmo/embedded reads it back. The go command running it, from
// its GOROOT source tree, is the one whose archives are embedded.
//
// Usage:
//
//	go tool embedstd [-V] [-go word]... -o blob
//
// The go command that builds the archives is this executable, or the command
// line the -go flags spell one word at a time, for a program that links the
// go command under a subcommand of its own.
package embedstd

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"cmd/internal/buildid"
	"cmd/internal/objabi"
	"internal/cosmo/embedded"
)

var flagSet = flag.NewFlagSet("embedstd", flag.ExitOnError)

var output = flagSet.String("o", "", "write the blob to `file`")

// goWords is the go command line, a word per -go flag; empty is this executable.
var goWords []string

func init() {
	flagSet.Func("go", "a `word` of the go command line that builds std; repeat for each word", func(word string) error {
		goWords = append(goWords, word)
		return nil
	})
}

// targets are the standard libraries one APE carries.
var targets = []struct{ goos, goarch string }{
	{"cosmo", "amd64"},
	{"cosmo", "arm64"},
}

// listed is the part of a go list -json record the manifest keeps.
type listed struct {
	ImportPath string
	Name       string
	Imports    []string
	Export     string
	BuildID    string
	Standard   bool
}

// Main runs embedstd with args, the command line after the program name,
// and answers its exit status.
func Main(args []string) int {
	objabi.Enter("embedstd", args, flagSet)
	objabi.AddVersionFlag()
	log.SetFlags(0)
	log.SetPrefix("embedstd: ")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: go tool embedstd [-V] [-go word]... -o blob\n")
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()
	if *output == "" || flag.NArg() != 0 {
		flag.Usage()
	}
	goCmd := goWords
	if len(goCmd) == 0 {
		exe, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}
		goCmd = []string{exe}
	}
	var writer embedded.Writer
	for _, target := range targets {
		name := target.goos + "_" + target.goarch
		packages := listStd(goCmd, target.goos, target.goarch)
		manifest := embedded.Manifest{Target: name}
		for _, pkg := range packages {
			entry := embedded.Package{ImportPath: pkg.ImportPath, Name: pkg.Name, Imports: pkg.Imports, BuildID: pkg.BuildID}
			if pkg.Export != "" {
				archive, err := os.ReadFile(pkg.Export)
				if err != nil {
					log.Fatalf("%s: %v", pkg.ImportPath, err)
				}
				archive, entry.BuildID = canonicalArchive(pkg, archive)
				entry.Archive = embedded.StdArchive(name, pkg.ImportPath)
				writer.Add(entry.Archive, archive)
			}
			manifest.Packages = append(manifest.Packages, entry)
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			log.Fatal(err)
		}
		writer.Add(embedded.ManifestEntry(name), data)
	}
	for _, header := range includeHeaders(gorootOf(goCmd)) {
		writer.Add(embedded.IncludeDir+"/"+header.name, header.data)
	}
	var blob bytes.Buffer
	if _, err := writer.WriteTo(&blob); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*output, blob.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	return 0
}

// goCommand starts the go command with args after its own words.
func goCommand(goCmd []string, args ...string) *exec.Cmd {
	return exec.Command(goCmd[0], append(append([]string{}, goCmd[1:]...), args...)...)
}

// header is one assembly header, under the name the blob files it by.
type header struct {
	name string
	data []byte
}

// includeHeaders answers the assembly headers of a GOROOT, in name order.
// A GOROOT that names a file is a go command carrying its standard library
// inside that file, and the headers come out of the blob it carries; a
// GOROOT that names a directory is a source tree, which keeps them under
// pkg/include.
func includeHeaders(goroot string) []header {
	info, err := os.Stat(goroot)
	if err != nil {
		log.Fatalf("reading the assembly headers: %v", err)
	}
	if !info.IsDir() {
		return embeddedHeaders(goroot)
	}
	return treeHeaders(filepath.Join(goroot, "pkg", "include"))
}

// treeHeaders reads the headers out of a source tree's include directory.
func treeHeaders(include string) []header {
	entries, err := os.ReadDir(include)
	if err != nil {
		log.Fatalf("reading the assembly headers: %v", err)
	}
	headers := make([]header, 0, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(include, entry.Name()))
		if err != nil {
			log.Fatal(err)
		}
		headers = append(headers, header{name: entry.Name(), data: data})
	}
	return headers
}

// embeddedHeaders reads the headers out of the blob the binary at exe
// carries.
func embeddedHeaders(exe string) []header {
	blob, err := embedded.OpenFile(exe)
	if err != nil {
		log.Fatalf("reading the assembly headers from %s: %v", exe, err)
	}
	prefix := embedded.IncludeDir + "/"
	names := blob.Entries(prefix)
	if len(names) == 0 {
		log.Fatalf("%s carries no assembly headers", exe)
	}
	headers := make([]header, 0, len(names))
	for _, name := range names {
		data, err := blob.ReadFile(name)
		if err != nil {
			log.Fatal(err)
		}
		headers = append(headers, header{name: strings.TrimPrefix(name, prefix), data: data})
	}
	return headers
}

// gorootOf answers the GOROOT the go command reads its source from.
func gorootOf(goCmd []string) string {
	out, err := goCommand(goCmd, "env", "GOROOT").Output()
	if err != nil {
		log.Fatalf("asking the go command for GOROOT: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// listStd builds the standard library for a target and answers every
// package in dependency order, with its archive and build ID.
func listStd(goCmd []string, goos, goarch string) []listed {
	cmd := goCommand(goCmd, "list", "-export", "-deps", "-json=ImportPath,Name,Imports,Export,BuildID,Standard", "std")
	// -trimpath, so a program built with it against these archives is the
	// program the source tree builds with it; the tree's path is not in them.
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "GOFLAGS=-trimpath", "CGO_ENABLED=0")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("listing std for %s/%s: %v", goos, goarch, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	var packages []listed
	for decoder.More() {
		var pkg listed
		if err := decoder.Decode(&pkg); err != nil {
			log.Fatalf("decoding go list output for %s/%s: %v", goos, goarch, err)
		}
		if !pkg.Standard {
			log.Fatalf("%s/%s: %s is not a standard package", goos, goarch, pkg.ImportPath)
		}
		sort.Strings(pkg.Imports)
		packages = append(packages, pkg)
	}
	return packages
}

// canonicalArchive answers the archive with a build ID derived from its own
// bytes, and that ID. The go command stamps an archive with an ID that hashes
// the compiler binary, so two compilers of one source stamp two IDs into the
// same object code. A blob built by each compiler in turn must converge, so
// the ID the blob carries names the content alone.
func canonicalArchive(pkg listed, archive []byte) ([]byte, string) {
	id, err := buildid.ReadFile(pkg.Export)
	if err != nil {
		log.Fatalf("%s: reading the archive's build ID: %v", pkg.ImportPath, err)
	}
	if id == "" {
		return archive, pkg.BuildID
	}
	matches, hash, err := buildid.FindAndHash(bytes.NewReader(archive), id, 0)
	if err != nil {
		log.Fatalf("%s: hashing the archive: %v", pkg.ImportPath, err)
	}
	canonical := contentID(id, hash)
	if err := buildid.Rewrite(sliceWriterAt(archive), matches, canonical); err != nil {
		log.Fatalf("%s: rewriting the archive's build ID: %v", pkg.ImportPath, err)
	}
	return archive, canonical
}

// contentID spells hash in the shape of id: the same number of parts, each of
// the same length, so the rewrite fits the bytes it replaces.
func contentID(id string, hash [32]byte) string {
	word := buildid.HashToString(hash)
	parts := strings.Split(id, "/")
	for idx, part := range parts {
		fill := word
		for len(fill) < len(part) {
			fill += word
		}
		parts[idx] = fill[:len(part)]
	}
	return strings.Join(parts, "/")
}

// sliceWriterAt writes into a byte slice in place.
type sliceWriterAt []byte

func (buf sliceWriterAt) WriteAt(data []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(data)) > int64(len(buf)) {
		return 0, fmt.Errorf("write of %d bytes at %d is outside %d bytes", len(data), off, len(buf))
	}
	return copy(buf[off:], data), nil
}
