// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"strings"
	"testing"

	"cmd/go/internal/load"
)

// toolGraph links a main package that imports cmd/compile, which imports
// cmd/compile/internal/ssa and the embedded standard package runtime, and
// answers the link action. mainID, ssaID and runtimeID are the content IDs
// of those packages. The packages carry no Deps, as under go build.
func toolGraph(mainID, ssaID, runtimeID string) *Action {
	pkg := func(path string, goroot bool) *load.Package {
		p := &load.Package{}
		p.ImportPath = path
		p.Goroot = goroot
		return p
	}
	runtime := &Action{Mode: "embedded std", Package: pkg("runtime", true), buildID: "a0/" + runtimeID}
	ssa := &Action{Mode: "build", Package: pkg("cmd/compile/internal/ssa", true), buildID: "a1/" + ssaID, Deps: []*Action{runtime}}
	check := &Action{Mode: "build check cache", Package: pkg("cmd/compile", true), Deps: []*Action{ssa}}
	compile := &Action{Mode: "build", Package: check.Package, buildID: "a2/c2", Deps: []*Action{ssa, check}}
	main := &Action{Mode: "build", Package: pkg("example.com/tool", false), buildID: "a3/" + mainID, Deps: []*Action{compile}}
	return &Action{Mode: "link", Package: main.Package, Deps: []*Action{main}}
}

// A tool's ID names its own packages. The main package that links it is
// not among them, so rebuilding that package leaves the ID alone, and a
// change inside the tool moves it.
func TestLinkedToolIDsFollowTheToolsPackages(t *testing.T) {
	first := linkedToolIDs(toolGraph("m1", "s1", "r1"))
	if !strings.HasPrefix(first, "compile=") || strings.Contains(first, ",") {
		t.Fatalf("linkedToolIDs = %q, want a single compile entry", first)
	}
	if again := linkedToolIDs(toolGraph("m2", "s1", "r1")); again != first {
		t.Errorf("the main package changed and the tool ID moved: %q became %q", first, again)
	}
	if moved := linkedToolIDs(toolGraph("m1", "s2", "r1")); moved == first {
		t.Errorf("a dependency of the tool changed and the tool ID stayed %q", first)
	}
	if moved := linkedToolIDs(toolGraph("m1", "s1", "r2")); moved == first {
		t.Errorf("an embedded standard package of the tool changed and the tool ID stayed %q", first)
	}
}

// A build leaves Package.Deps empty. The ID must still follow a dependency
// through the action graph, or a changed linker keeps its old ID.
func TestLinkedToolIDsFollowActionDepsWithoutPackageDeps(t *testing.T) {
	graph := func(ldID string) *Action {
		pkg := func(path string) *load.Package {
			pkgObj := &load.Package{}
			pkgObj.ImportPath = path
			pkgObj.Goroot = true
			return pkgObj
		}
		ldAct := &Action{Mode: "build", Package: pkg("cmd/link/internal/ld"), buildID: "a1/" + ldID}
		link := &Action{Mode: "build", Package: pkg("cmd/link"), buildID: "a2/c2", Deps: []*Action{ldAct}}
		main := &Action{Mode: "build", Package: pkg("example.com/tool"), buildID: "a3/m1", Deps: []*Action{link}}
		main.Package.Goroot = false
		return &Action{Mode: "link", Package: main.Package, Deps: []*Action{main}}
	}
	first := linkedToolIDs(graph("l1"))
	if !strings.HasPrefix(first, "link=") {
		t.Fatalf("linkedToolIDs = %q, want a link entry", first)
	}
	if moved := linkedToolIDs(graph("l2")); moved == first {
		t.Errorf("cmd/link/internal/ld changed and the link ID stayed %q", first)
	}
}

// ld stamps the linked tool IDs into the binary. A link whose stamp changes
// must not reuse the cached binary that carries the one.
func TestLinkActionIDCoversLinkedToolIDs(t *testing.T) {
	var builder Builder
	builder.toolIDCache.Do("link", func() string { return "linktool" })
	first := builder.linkActionID(toolGraph("m1", "s1"))
	if moved := builder.linkActionID(toolGraph("m1", "s2")); moved == first {
		t.Errorf("a linked tool changed and the link action ID stayed %x", first)
	}
}

// A binary that links no tool stamps nothing.
func TestLinkedToolIDsAbsentWithoutTools(t *testing.T) {
	p := &load.Package{}
	p.ImportPath = "example.com/plain"
	main := &Action{Mode: "build", Package: p, buildID: "a/b"}
	root := &Action{Mode: "link", Package: p, Deps: []*Action{main}}
	if got := linkedToolIDs(root); got != "" {
		t.Errorf("linkedToolIDs = %q, want none", got)
	}
}
