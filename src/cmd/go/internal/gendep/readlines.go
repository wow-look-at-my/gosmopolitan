// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package gendep

import (
	"bytes"
	"os"
)

// readLines calls visit with each line of the file at path, read whole into
// memory. It is the reader where no mapping is available.
func readLines(path string, visit func(line []byte) bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for line := range bytes.Lines(data) {
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
		if !visit(line) {
			return nil
		}
	}
	return nil
}
