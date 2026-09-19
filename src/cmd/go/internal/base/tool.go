// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package base

import (
	"fmt"
	"go/build"
	"os"
	"path/filepath"

	"cmd/go/internal/cfg"
	"cmd/internal/par"
)

// self is this executable when it links build tools, and selfTools names
// them. A linked tool runs as "<self> tool <name>" instead of a file under
// build.ToolDir.
var (
	self      string
	selfTools = map[string]struct{}{}
)

// SetSelf records that exe, this executable, links the named tools.
func SetSelf(exe string, tools []string) {
	self = exe
	for _, name := range tools {
		selfTools[name] = struct{}{}
	}
}

// goCommand is the argv prefix that starts this go command again, when a
// binary of another name links it and reaches it as "<self> go".
var goCommand []string

// SetGoCommand records the argv prefix that starts this go command again.
func SetGoCommand(argv []string) {
	goCommand = argv
}

// GoCommand answers the argv prefix that starts this go command again: the
// recorded prefix, or this executable alone.
func GoCommand() ([]string, error) {
	if len(goCommand) > 0 {
		return goCommand, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return []string{exe}, nil
}

// Linked reports whether this executable links the named tool.
func Linked(toolName string) bool {
	_, found := selfTools[toolName]
	return found
}

// Tool returns the path to the named builtin tool (for example, "vet"): this
// executable for a linked tool. If the tool cannot be found, Tool exits the
// process.
func Tool(toolName string) string {
	toolPath, err := ToolPath(toolName)
	if err != nil {
		// Give a nice message if there is no tool with that name.
		fmt.Fprintf(os.Stderr, "go: no such tool %q\n", toolName)
		SetExitStatus(2)
		Exit()
	}
	return toolPath
}

// ToolCmd returns the command line that starts the named builtin tool, before
// the tool's own arguments: "<self> tool <name>" for a linked tool, the tool's
// path otherwise. If the tool cannot be found, ToolCmd exits the process.
func ToolCmd(toolName string) []string {
	if Linked(toolName) {
		return []string{self, "tool", toolName}
	}
	return []string{Tool(toolName)}
}

// ToolPath returns the path at which we expect to find the named tool
// (for example, "vet"), and the error (if any) from statting that path.
// A linked tool is this executable and is never stat'ed.
func ToolPath(toolName string) (string, error) {
	if !ValidToolName(toolName) {
		return "", fmt.Errorf("bad tool name: %q", toolName)
	}
	if Linked(toolName) {
		return self, nil
	}
	toolPath := filepath.Join(build.ToolDir, toolName) + cfg.ToolExeSuffix()
	err := toolStatCache.Do(toolPath, func() error {
		_, err := os.Stat(toolPath)
		return err
	})
	return toolPath, err
}

func ValidToolName(toolName string) bool {
	if toolName == "" {
		return false
	}
	for _, c := range toolName {
		switch {
		case 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}

var toolStatCache par.Cache[string, error]
