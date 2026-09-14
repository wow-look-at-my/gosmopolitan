// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bio

import (
	"io"
	"os"

	"internal/cosmo/embedded"
)

// OpenSection returns a Reader over the size bytes of f starting at offset.
// Offsets the Reader reports and seeks to stay absolute in f, so a caller
// that maps the file by them maps the right bytes; reads end at the
// section's end.
func OpenSection(f *os.File, offset, size int64) *Reader {
	section := io.NewSectionReader(f, offset, size)
	return &Reader{f: f, rs: section, base: offset, Reader: newBufio(section)}
}

// OpenAny opens name: an entry of this binary's embedded standard library
// when it carries the self: prefix, a file otherwise.
func OpenAny(name string) (*Reader, error) {
	if !embedded.IsSelf(name) {
		return Open(name)
	}
	f, offset, size, err := embedded.Open(name)
	if err != nil {
		return nil, err
	}
	return OpenSection(f, offset, size), nil
}
