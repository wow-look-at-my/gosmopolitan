// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"bytes"
	"fmt"
	"internal/pkgbits"
	"io"
	"os"

	"cmd/compile/internal/base"
	"cmd/internal/archive"
	"cmd/internal/goobj"
	"cmd/internal/obj"
)

// KeepReplacedIndices gives a package compiled with its test files the symbol
// indices of the same package compiled without them, when -testvariant names
// that compile's archive. A test binary links this object in place of that
// one, and every package compiled against that one refers to its symbols by
// those indices.
func KeepReplacedIndices() {
	if base.Flag.TestVariant == "" {
		return
	}
	index, count, err := readDefIndices(base.Flag.TestVariant)
	if err != nil {
		base.Fatalf("-testvariant: %v", err)
	}
	if err := base.Ctxt.KeepDefIndices(index, count); err != nil {
		base.Fatalf("-testvariant %s: package %s %v", base.Flag.TestVariant, base.Ctxt.Pkgpath, err)
	}
}

// readDefIndices answers the index of every non-file-local symbol the Go
// object in an archive defines, and how many symbols it defines.
func readDefIndices(path string) (map[obj.DefKey]int32, int, error) {
	reader, err := readGoObject(path)
	if err != nil {
		return nil, 0, err
	}
	count := reader.NSym()
	index := make(map[obj.DefKey]int32, count)
	for idx := 0; idx < count; idx++ {
		// A file-local or unnamed symbol is only ever referred to from inside
		// its own package, so its index is free to move.
		sym := reader.Sym(uint32(idx))
		if sym.ABI() == goobj.SymABIstatic || sym.NameLen(reader) == 0 {
			continue
		}
		index[obj.DefKey{Name: sym.Name(reader), ABI: obj.ABI(sym.ABI())}] = int32(idx)
	}
	return index, count, nil
}

// dumpExportData writes the export data and records its fingerprint. Under
// -testvariant the fingerprint is the replaced package's: every package
// compiled against that one recorded it, and the linker checks it against the
// object it actually loads.
func dumpExportData(pw *pkgbits.PkgEncoder, out io.Writer) {
	if base.Flag.TestVariant == "" {
		base.Ctxt.Fingerprint = pw.DumpTo(out)
		return
	}
	reader, err := readGoObject(base.Flag.TestVariant)
	if err != nil {
		base.Fatalf("-testvariant: %v", err)
	}
	fingerprint := reader.Fingerprint()
	var buf bytes.Buffer
	pw.DumpTo(&buf)
	data := buf.Bytes()
	copy(data[len(data)-len(fingerprint):], fingerprint[:])
	if _, err := out.Write(data); err != nil {
		base.Fatalf("writing export data: %v", err)
	}
	base.Ctxt.Fingerprint = fingerprint
}

// readGoObject opens the object the compiler wrote into an archive.
func readGoObject(path string) (*goobj.Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	arch, err := archive.Parse(file, false)
	if err != nil {
		return nil, err
	}
	for _, entry := range arch.Entries {
		if entry.Type != archive.EntryGoObj || entry.Name != "_go_.o" {
			continue
		}
		data := make([]byte, entry.Obj.Size)
		if _, err := file.ReadAt(data, entry.Obj.Offset); err != nil && err != io.EOF {
			return nil, err
		}
		reader := goobj.NewReaderFromBytes(data, false)
		if reader == nil {
			return nil, fmt.Errorf("%s: _go_.o is not a Go object", path)
		}
		return reader, nil
	}
	return nil, fmt.Errorf("%s holds no compiled Go object", path)
}
