// Command submodulebranch moves each submodule that follows this repository's
// branch onto that branch, and writes the version it lands on into the files
// that record it.
//
// make.bash runs it before the build, so a build reads the branch a change is
// on rather than a commit somebody wrote down once. The command is built by the
// bootstrap toolchain in GOPATH mode, so it imports the standard library alone.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// versioned are the files that record a module's version beside its source. The
// go command refuses to build in vendor mode when they disagree.
var versioned = []string{
	filepath.Join("cmd", "go.mod"),
	filepath.Join("cmd", "vendor", "modules.txt"),
}

func main() {
	root := flag.String("C", ".", "the repository root to work in")
	flag.Parse()

	src, err := os.ReadFile(filepath.Join(*root, ".gitmodules"))
	if err != nil {
		// A source tree with no .gitmodules has nothing to follow. A release
		// tarball is one: it ships the submodule content already.
		return
	}

	here := Here(Git)
	for _, m := range Modules(src) {
		version, err := Follow(Git, *root, m, here)
		if err != nil {
			// The checkout stays as it is, so a build with no network reads
			// what it already has.
			fmt.Fprintf(os.Stderr, "submodulebranch: %s stays where it is: %v\n", m.Path, err)
			continue
		}
		module, err := modulePath(filepath.Join(*root, m.Path))
		if err != nil {
			fmt.Fprintf(os.Stderr, "submodulebranch: %s: %v\n", m.Path, err)
			continue
		}
		for _, f := range versioned {
			if err := Rewrite(filepath.Join(*root, "src", f), module, version); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "submodulebranch: %s: %v\n", f, err)
			}
		}
		fmt.Fprintf(os.Stderr, "submodulebranch: %s at %s\n", m.Path, version)
	}
}

// modulePath reads the module path a submodule's own go.mod declares. The
// version files name the module, and a submodule's directory does not have to
// spell it.
func modulePath(dir string) (string, error) {
	src, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	return ModulePath(src)
}
