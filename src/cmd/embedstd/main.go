// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// Embedstd writes the blob a go binary carries its standard library in: the
// compiled archive of every standard package for cosmo/amd64 and cosmo/arm64.
// This also covers js/wasm and wasip1/wasm. The assembly headers under
// pkg/include, and a manifest per target naming each package, its imports and
// its build ID. The linker's -apeappend flag puts the blob past an APE's load
// span, and internal/cosmo/embedded reads it back. The go command running it,
// from its GOROOT source tree, is the one whose archives are embedded.
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

// cgoOn builds std with cgo on, so the blob carries runtime/cgo compiled by the cosmocc compiler of each architecture.
var cgoOn = flagSet.Bool("cgo", true, "build std with cgo on; the cosmocc compiler of each architecture must be on PATH")

// goWords is the go command line, a word per -go flag; empty is this executable.
var goWords []string

func init() {
	flagSet.Func("go", "a `word` of the go command line that builds std; repeat for each word", func(word string) error {
		goWords = append(goWords, word)
		return nil
	})
}

// targets are the standard libraries one APE carries. cgo says the target's
// std is built with cgo when -cgo is on; a wasm target has no C compiler.
var targets = []struct {
	goos, goarch string
	cgo          bool
}{
	{"cosmo", "amd64", true},
	{"cosmo", "arm64", true},
	{"js", "wasm", false},
	{"wasip1", "wasm", false},
}

// listed is the part of a go list -json record the manifest keeps, with the
// errors -e lets through.
type listed struct {
	ImportPath string
	Name       string
	Imports    []string
	Export     string
	BuildID    string
	Standard   bool
	Error      *listError
	DepsErrors []*listError
}

// listError is a go list -json error record.
type listError struct {
	ImportStack []string
	Err         string
}

// Main runs embedstd with args, the command line after the program name,
// and answers its exit status.
func Main(args []string) int {
	objabi.Enter("embedstd", args, flagSet)
	objabi.AddVersionFlag()
	log.SetFlags(0)
	log.SetPrefix("embedstd: ")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: go tool embedstd [-V] [-cgo=false] [-go word]... -o blob\n")
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
		cgo := *cgoOn && target.cgo
		if cgo {
			requireCompiler(goCmd, target.goos, target.goarch)
		}
		packages := listStd(goCmd, target.goos, target.goarch, cgo)
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
	if err := addHeaders(&writer, goCmd); err != nil {
		log.Fatalf("reading the assembly headers: %v", err)
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

// addHeaders puts the assembly headers of the go command into the blob.
//
// A go command that carries its own standard library has no GOROOT directory.
// GOROOT names the executable, and pkg/include is an entry of the blob that
// executable carries. This tool runs inside that executable, so its own blob
// is where those headers come from. A go command with a GOROOT on disk keeps
// reading the directory.
func addHeaders(writer *embedded.Writer, goCmd []string) error {
	include := filepath.Join(gorootOf(goCmd), "pkg", "include")
	headers, err := os.ReadDir(include)
	if err == nil {
		for _, header := range headers {
			data, err := os.ReadFile(filepath.Join(include, header.Name()))
			if err != nil {
				return err
			}
			writer.Add(embedded.IncludeDir+"/"+header.Name(), data)
		}
		return nil
	}
	carried, listErr := embedded.Entries(embedded.IncludeDir + "/")
	if listErr != nil || len(carried) == 0 {
		return err
	}
	for _, name := range carried {
		data, readErr := embedded.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		writer.Add(name, data)
	}
	return nil
}

// goCommand starts the go command with args after its own words.
func goCommand(goCmd []string, args ...string) *exec.Cmd {
	return exec.Command(goCmd[0], append(append([]string{}, goCmd[1:]...), args...)...)
}

// gorootOf answers the GOROOT the go command reads its source from.
func gorootOf(goCmd []string) string {
	out, err := goCommand(goCmd, "env", "GOROOT").Output()
	if err != nil {
		log.Fatalf("asking the go command for GOROOT: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// requireCompiler stops before any build when the C compiler of a target is
// not on PATH. The listing below tolerates a package that fails, and a blob
// with no runtime/cgo would otherwise pass as whole.
func requireCompiler(goCmd []string, goos, goarch string) {
	cmd := goCommand(goCmd, "env", "CC")
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=1")
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("asking the go command for the %s/%s C compiler: %v", goos, goarch, err)
	}
	compiler := strings.Fields(strings.TrimSpace(string(out)))
	if len(compiler) == 0 {
		log.Fatalf("the go command names no C compiler for %s/%s", goos, goarch)
	}
	if _, err := exec.LookPath(compiler[0]); err != nil {
		log.Fatalf("cgo is on and the %s/%s C compiler %q is not on PATH; put it there or pass -cgo=false", goos, goarch, compiler[0])
	}
}

// listStd builds the standard library for a target, with cgo on or off, and
// answers every package in dependency order. That listStd is with its
// archive and build ID.
func listStd(goCmd []string, goos, goarch string, cgo bool) []listed {
	// -e: a handful of standard packages hold nothing but tests.
	args := []string{"list", "-e", "-export", "-deps", "-json=ImportPath,Name,Imports,Export,BuildID,Standard,Error,DepsErrors", "std"}
	var progress *fileLines
	if os.Getenv(progressEnv) == "1" {
		// -x echoes each tool command, and a command line names the files it reads. It is not in any cache key.
		args = append([]string{"list", "-x"}, args[1:]...)
		progress = &fileLines{target: goos + "/" + goarch, out: os.Stderr}
	}
	cmd := goCommand(goCmd, args...)
	cgoEnabled := "0"
	if cgo {
		cgoEnabled = "1"
	}
	// -trimpath, so a program built with it against these archives.
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "GOFLAGS=-trimpath", "CGO_ENABLED="+cgoEnabled)
	cmd.Stderr = os.Stderr
	if progress != nil {
		cmd.Stderr = progress
	}
	out, err := cmd.Output()
	if err != nil {
		if progress != nil {
			os.Stderr.Write(progress.all.Bytes())
		}
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
		// -e keeps a package that failed in the listing, with no archive. Only a test-only package may have none.
		if pkg.Error != nil && !strings.Contains(pkg.Error.Err, "no non-test Go files") {
			log.Fatalf("%s/%s: %s: %s", goos, goarch, pkg.ImportPath, strings.TrimSpace(pkg.Error.Err))
		}
		if len(pkg.DepsErrors) > 0 {
			log.Fatalf("%s/%s: %s: a dependency failed: %s", goos, goarch, pkg.ImportPath, strings.TrimSpace(pkg.DepsErrors[0].Err))
		}
		sort.Strings(pkg.Imports)
		packages = append(packages, pkg)
	}
	return packages
}

// canonicalArchive answers the archive with a build ID derived from its own
// bytes, and that ID. The go command stamps an archive with an ID that hashes
// the compiler binary, so compilers of one source stamp IDs. Into the same
// object code. A blob built by each compiler in turn must converge, so the ID
// the blob carries names the content alone.
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

// contentID spells hash in the shape of id. The same number of parts, each of
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
