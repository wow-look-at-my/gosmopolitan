// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Embedstd writes the blob a go binary carries its standard library in:
// the compiled archive of every standard package for cosmo/amd64 and
// cosmo/arm64, the assembly headers under pkg/include, and a manifest per
// target naming each package, its imports and its build ID. The linker's
// -apeappend flag puts the blob past an APE's load span, and
// internal/cosmo/embedded reads it back.
//
// Usage:
//
//	go tool embedstd [-V] -o blob
//
// The go command that runs it is the one whose archives are embedded, from
// its GOROOT source tree.
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
	"runtime"
	"sort"

	"cmd/internal/objabi"
	"internal/cosmo/embedded"
)

var flagSet = flag.NewFlagSet("embedstd", flag.ExitOnError)

var output = flagSet.String("o", "", "write the blob to `file`")

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
		fmt.Fprintf(os.Stderr, "usage: go tool embedstd [-V] -o blob\n")
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()
	if *output == "" || flag.NArg() != 0 {
		flag.Usage()
	}
	goCmd, err := os.Executable()
	if err != nil {
		log.Fatal(err)
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
	include := filepath.Join(runtime.GOROOT(), "pkg", "include")
	headers, err := os.ReadDir(include)
	if err != nil {
		log.Fatalf("reading the assembly headers: %v", err)
	}
	for _, header := range headers {
		data, err := os.ReadFile(filepath.Join(include, header.Name()))
		if err != nil {
			log.Fatal(err)
		}
		writer.Add(embedded.IncludeDir+"/"+header.Name(), data)
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

// listStd builds the standard library for a target and answers every
// package in dependency order, with its archive and build ID.
func listStd(goCmd, goos, goarch string) []listed {
	cmd := exec.Command(goCmd, "list", "-export", "-deps", "-json=ImportPath,Name,Imports,Export,BuildID,Standard", "std")
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "GOFLAGS=", "CGO_ENABLED=0")
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
