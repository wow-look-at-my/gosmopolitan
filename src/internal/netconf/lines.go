// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package netconf

import "io"

// lineBuffer bounds a line. A longer line ends the read, as it always has in
// package net.
const lineBuffer = 64 * 1024

// Fields splits line at spaces, tabs, carriage returns and newlines.
func Fields(line string) []string {
	var fields []string
	last := 0
	for idx := 0; idx < len(line); idx++ {
		switch line[idx] {
		case ' ', '\r', '\t', '\n':
			if last < idx {
				fields = append(fields, line[last:idx])
			}
			last = idx + 1
		}
	}
	if last < len(line) {
		fields = append(fields, line[last:])
	}
	return fields
}

// lineReader hands out the lines of r, the last without a newline after it.
type lineReader struct {
	r     io.Reader
	data  []byte
	atEOF bool
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{r: r, data: make([]byte, 0, lineBuffer)}
}

func (lines *lineReader) fromData() (string, bool) {
	for idx, char := range lines.data {
		if char == '\n' {
			line := string(lines.data[:idx])
			rest := copy(lines.data, lines.data[idx+1:])
			lines.data = lines.data[:rest]
			return line, true
		}
	}
	if lines.atEOF && len(lines.data) > 0 {
		line := string(lines.data)
		lines.data = lines.data[:0]
		return line, true
	}
	return "", false
}

func (lines *lineReader) next() (string, bool) {
	if line, ok := lines.fromData(); ok {
		return line, true
	}
	if have := len(lines.data); have < cap(lines.data) {
		num, err := io.ReadFull(lines.r, lines.data[have:cap(lines.data)])
		lines.data = lines.data[:have+num]
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			lines.atEOF = true
		}
	}
	return lines.fromData()
}
