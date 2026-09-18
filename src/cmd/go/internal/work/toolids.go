// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"cmd/internal/buildid"
)

// linkedToolIDs answers the tool IDs of the tools linked into root's binary,
// as "name=id" pairs sorted by name and joined by commas, or "" when root
// links no tool. A tool is a GOROOT package directly under cmd, and its ID
// hashes the content IDs of its package and every package it depends on.
//
// The ID describes the tool's code and nothing else. A binary that links the
// go command and its tools is rebuilt whenever any of its own code changes,
// and the tool ID must not: a compiler that is the same code reads the same
// cache entries from whichever binary carries it. Builds of the same tool by
// different compilers converge once their archives do.
func linkedToolIDs(root *Action) string {
	contentIDs := map[string]string{}
	var tools []*Action
	seen := map[*Action]bool{}
	var walk func(a *Action)
	walk = func(a *Action) {
		if seen[a] {
			return
		}
		seen[a] = true
		if a.Mode == "build" && a.Package != nil && a.buildID != "" {
			contentIDs[a.Package.ImportPath] = contentID(a.buildID)
			if isToolPackage(a.Package.ImportPath) && a.Package.Goroot {
				tools = append(tools, a)
			}
		}
		for _, dep := range a.Deps {
			walk(dep)
		}
	}
	walk(root)
	if len(tools) == 0 {
		return ""
	}

	var pairs []string
	for _, tool := range tools {
		h := sha256.New()
		fmt.Fprintf(h, "%s %s\n", tool.Package.ImportPath, contentIDs[tool.Package.ImportPath])
		deps := append([]string(nil), tool.Package.Deps...)
		sort.Strings(deps)
		for _, dep := range deps {
			if id, ok := contentIDs[dep]; ok {
				fmt.Fprintf(h, "%s %s\n", dep, id)
			}
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		name := strings.TrimPrefix(tool.Package.ImportPath, "cmd/")
		pairs = append(pairs, name+"="+buildid.HashToString(sum))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

// isToolPackage reports whether path names a tool: cmd/<name> and nothing
// deeper.
func isToolPackage(path string) bool {
	rest, ok := strings.CutPrefix(path, "cmd/")
	return ok && rest != "" && !strings.Contains(rest, "/")
}
