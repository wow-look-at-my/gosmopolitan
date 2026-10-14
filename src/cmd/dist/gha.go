// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// toolWarning matches a tool's "file:line[:col]: ... warning: ..." line.
var toolWarning = regexp.MustCompile(`^(\S+?):(\d+)(?::(\d+))?: ((?:\S+ )*warning: .*)$`)

// annotateWarnings turns each warning line of a tool's output into a GitHub
// Actions ::warning command when dist runs in GitHub Actions. A relative file
// name resolves against dir. cmd/go's work package does the same for the
// commands it runs.
func annotateWarnings(out []byte, dir string) []byte {
	if os.Getenv("GITHUB_ACTIONS") != "true" || !strings.Contains(string(out), "warning: ") {
		return out
	}
	workspace := os.Getenv("GITHUB_WORKSPACE")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	lines := strings.Split(string(out), "\n")
	for idx, line := range lines {
		match := toolWarning.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		file := match[1]
		// A -trimpath build names a file by its module path, which is not under dir.
		if joined := filepath.Join(dir, file); !filepath.IsAbs(file) && dir != "" && fileExists(joined) {
			file = joined
		}
		if workspace != "" {
			if rel, err := filepath.Rel(workspace, file); err == nil && !strings.HasPrefix(rel, "..") {
				file = rel
			}
		}
		props := "file=" + ghaProperty(filepath.ToSlash(file)) + ",line=" + match[2]
		if match[3] != "" {
			props += ",col=" + match[3]
		}
		lines[idx] = "::warning " + props + "::" + ghaData(match[4])
	}
	return []byte(strings.Join(lines, "\n"))
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// ghaData escapes a workflow command's message.
func ghaData(text string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(text)
}

// ghaProperty escapes a workflow command's property value.
func ghaProperty(text string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(text)
}
