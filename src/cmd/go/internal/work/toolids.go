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
	var tools []*Action
	seen := map[*Action]bool{}
	var walk func(act *Action)
	walk = func(act *Action) {
		if seen[act] {
			return
		}
		seen[act] = true
		if act.Mode == "build" && act.Package != nil && act.buildID != "" &&
			isToolPackage(act.Package.ImportPath) && act.Package.Goroot {
			tools = append(tools, act)
		}
		for _, dep := range act.Deps {
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
		fmt.Fprintf(h, "%s %s\n", tool.Package.ImportPath, contentID(tool.buildID))
		ids := packageContentIDs(tool)
		deps := make([]string, 0, len(ids))
		for dep := range ids {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			fmt.Fprintf(h, "%s %s\n", dep, ids[dep])
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		name := strings.TrimPrefix(tool.Package.ImportPath, "cmd/")
		pairs = append(pairs, name+"="+buildid.HashToString(sum))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

// packageContentIDs answers the content ID of each package the tool's action
// graph compiles or reads, by import path, without the tool's own package.
// It reads the action graph, because go build leaves Package.Deps empty. Only
// go list fills that field.
func packageContentIDs(tool *Action) map[string]string {
	ids := map[string]string{}
	seen := map[*Action]bool{tool: true}
	var walk func(act *Action)
	walk = func(act *Action) {
		for _, dep := range act.Deps {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			if (dep.Mode == "build" || dep.Mode == "embedded std") && dep.Package != nil && dep.buildID != "" {
				ids[dep.Package.ImportPath] = contentID(dep.buildID)
			}
			walk(dep)
		}
	}
	walk(tool)
	delete(ids, tool.Package.ImportPath)
	return ids
}

// isToolPackage reports whether path names a tool: cmd/<name> and nothing
// deeper.
func isToolPackage(path string) bool {
	rest, ok := strings.CutPrefix(path, "cmd/")
	return ok && rest != "" && !strings.Contains(rest, "/")
}
