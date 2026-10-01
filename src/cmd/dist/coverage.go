// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// the block into the step summary.
const coverageHeading = "##### Test coverage"

// vendoredThirdParty reports whether importPath is in the vendor tree of std
// or cmd and comes from a module outside wow-look-at-my. The go command
// ignores the test files of such a package; see cmd/go/internal/load.
func vendoredThirdParty(importPath string) bool {
	rest, ok := strings.CutPrefix(importPath, "vendor/")
	if !ok {
		rest, ok = strings.CutPrefix(importPath, "cmd/vendor/")
	}
	return ok && !strings.HasPrefix(rest, "github.com/wow-look-at-my/")
}

// untestedModule is one vendored module whose test files the go command
// ignores.
type untestedModule struct {
	path      string
	packages  int
	testFiles int
}

// coverage counts the packages of std and cmd by whether their tests run.
type coverage struct {
	total    int
	tested   int
	untested []untestedModule
}

// untestedPackages returns the number of packages that have test files the
// go command ignores.
func (c coverage) untestedPackages() int {
	num := 0
	for _, mod := range c.untested {
		num += mod.packages
	}
	return num
}

// format returns the report as Markdown.
func (c coverage) format() string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "%s\n\n", coverageHeading)
	fmt.Fprintf(&buf, "**%.1f%% of packages have tests that run** (%d of %d in std and cmd).\n\n",
		percent(c.tested, c.total), c.tested, c.total)
	untested := c.untestedPackages()
	if untested == 0 {
		buf.WriteString("Every package with test files runs them.\n")
		return buf.String()
	}
	fmt.Fprintf(&buf, "%d packages (%.1f%%) have test files that cannot run. ", untested, percent(untested, c.total))
	buf.WriteString("Each is a vendored third-party module whose tests need files or packages that the vendor tree does not have.\n\n")
	buf.WriteString("| module | packages | test files |\n")
	buf.WriteString("| --- | ---: | ---: |\n")
	for _, mod := range c.untested {
		fmt.Fprintf(&buf, "| `%s` | %d | %d |\n", mod.path, mod.packages, mod.testFiles)
	}
	return buf.String()
}

func percent(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

// measureCoverage lists std and cmd with the go command at goBin.
func measureCoverage(goBin string, tags []string) (coverage, error) {
	const format = `{{.ImportPath}}|{{with .Module}}{{.Path}}{{end}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}|{{range .IgnoredGoFiles}}{{.}} {{end}}`
	cmd := exec.Command(goBin, "list")
	cmd.Args = append(cmd.Args, tags...)
	cmd.Args = append(cmd.Args, "-f", format, "std", "cmd")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return coverage{}, fmt.Errorf("go list std cmd: %v\n%s", err, stderr.Bytes())
	}
	return parseCoverage(string(out))
}

// parseCoverage reads the lines that measureCoverage asks go list for.
func parseCoverage(out string) (coverage, error) {
	var cov coverage
	modules := map[string]*untestedModule{}
	for line := range strings.Lines(out) {
		fields := strings.SplitN(strings.TrimSuffix(line, "\n"), "|", 5)
		if len(fields) != 5 {
			return coverage{}, fmt.Errorf("go list printed %q, want five fields", line)
		}
		importPath, modPath, ignored := fields[0], fields[1], fields[4]
		var testFiles, xtestFiles int
		if _, err := fmt.Sscan(fields[2]+" "+fields[3], &testFiles, &xtestFiles); err != nil {
			return coverage{}, fmt.Errorf("go list printed %q: %v", line, err)
		}
		cov.total++
		if testFiles+xtestFiles > 0 {
			cov.tested++
			continue
		}
		if !vendoredThirdParty(importPath) {
			continue
		}
		ignoredTests := 0
		for _, name := range strings.Fields(ignored) {
			if strings.HasSuffix(name, "_test.go") {
				ignoredTests++
			}
		}
		if ignoredTests == 0 {
			continue
		}
		if modPath == "" {
			modPath = vendoredModuleGuess(importPath)
		}
		mod := modules[modPath]
		if mod == nil {
			mod = &untestedModule{path: modPath}
			modules[modPath] = mod
		}
		mod.packages++
		mod.testFiles += ignoredTests
	}
	for _, mod := range modules {
		cov.untested = append(cov.untested, *mod)
	}
	sort.Slice(cov.untested, func(left, right int) bool {
		if cov.untested[left].packages != cov.untested[right].packages {
			return cov.untested[left].packages > cov.untested[right].packages
		}
		return cov.untested[left].path < cov.untested[right].path
	})
	return cov, nil
}

// vendoredModuleGuess names the module of a vendored package when go list
// reports none: the first elements of the path under vendor.
func vendoredModuleGuess(importPath string) string {
	rest, ok := strings.CutPrefix(importPath, "cmd/vendor/")
	if !ok {
		rest = strings.TrimPrefix(importPath, "vendor/")
	}
	elems := strings.SplitN(rest, "/", 4)
	if len(elems) > 3 {
		elems = elems[:3]
	}
	return strings.Join(elems, "/")
}
