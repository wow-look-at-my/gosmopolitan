// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package embedstd

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
)

// An older embedstd ignores this variable. It would fail on an unknown flag.
const progressEnv = "GOEMBEDSTD_PROGRESS"

// fileLines reads the -x echo of a go command. For each compile or asm
// command it writes one line per source file to out.
type fileLines struct {
	target  string
	out     io.Writer
	all     bytes.Buffer
	partial []byte
}

func (lines *fileLines) Write(data []byte) (int, error) {
	lines.all.Write(data)
	lines.partial = append(lines.partial, data...)
	for {
		end := bytes.IndexByte(lines.partial, '\n')
		if end < 0 {
			return len(data), nil
		}
		lines.echo(string(lines.partial[:end]))
		lines.partial = lines.partial[end+1:]
	}
}

// echo writes the source files of one -x line, when it runs a compiler or
// an assembler.
func (lines *fileLines) echo(line string) {
	pkg, files := toolFiles(line)
	for _, file := range files {
		fmt.Fprintf(lines.out, "embedstd: %s %s/%s\n", lines.target, pkg, strings.TrimPrefix(file, "./"))
	}
}

// toolFiles answers the package and the source files of a compile or asm
// command line, or no files for any other line.
func toolFiles(line string) (string, []string) {
	fields := strings.Fields(line)
	tool := -1
	switch {
	case len(fields) > 0 && isBuildTool(path.Base(fields[0])):
		tool = 0
	case len(fields) > 2 && fields[1] == "tool" && isBuildTool(fields[2]):
		tool = 2
	default:
		return "", nil
	}
	var pkg string
	var files []string
	for idx := tool + 1; idx < len(fields); idx++ {
		switch field := fields[idx]; {
		case field == "-p" && idx+1 < len(fields):
			pkg = fields[idx+1]
			idx++
		case strings.HasPrefix(field, "./") && (strings.HasSuffix(field, ".go") || strings.HasSuffix(field, ".s")):
			files = append(files, field)
		}
	}
	if pkg == "" {
		return "", nil
	}
	return pkg, files
}

// isBuildTool reports whether name is a tool that reads source files.
func isBuildTool(name string) bool {
	return name == "compile" || name == "asm"
}
