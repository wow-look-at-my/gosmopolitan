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
		if isBuiltPackage(act) && isToolPackage(act.Package.ImportPath) && act.Package.Goroot {
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
		// The dependencies come from the action graph.
		contentIDs := packageContentIDs(tool)
		paths := make([]string, 0, len(contentIDs))
		for path := range contentIDs {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		hash := sha256.New()
		for _, path := range paths {
			fmt.Fprintf(hash, "%s %s\n", path, contentIDs[path])
		}
		var sum [32]byte
		copy(sum[:], hash.Sum(nil))
		name := strings.TrimPrefix(tool.Package.ImportPath, "cmd/")
		pairs = append(pairs, name+"="+buildid.HashToString(sum))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

// packageContentIDs answers the content ID of top and of every package
// reachable from it in the action graph, by import path. It reads the action
// graph, because go build leaves Package.Deps empty. Only go list fills that
// field.
func packageContentIDs(top *Action) map[string]string {
	ids := map[string]string{}
	seen := map[*Action]bool{}
	var walk func(act *Action)
	walk = func(act *Action) {
		if seen[act] {
			return
		}
		seen[act] = true
		if isBuiltPackage(act) || (act.Mode == "embedded std" && act.Package != nil && act.buildID != "") {
			ids[act.Package.ImportPath] = contentID(act.buildID)
		}
		for _, dep := range act.Deps {
			walk(dep)
		}
	}
	walk(top)
	return ids
}

// isBuiltPackage reports whether act compiled a package and knows its build ID.
func isBuiltPackage(act *Action) bool {
	return act.Mode == "build" && act.Package != nil && act.buildID != ""
}

// isToolPackage reports whether path names a tool: cmd/<name> and nothing
// deeper.
func isToolPackage(path string) bool {
	rest, ok := strings.CutPrefix(path, "cmd/")
	return ok && rest != "" && !strings.Contains(rest, "/")
}
