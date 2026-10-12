// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/cosmoape"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"os"
)

// cmd/dist compiles the loaders into apeld/bin before it builds this package. Git does not track them.

//go:embed apeld/bin/apeld-linux-amd64
var apeldLinuxAMD64 []byte

//go:embed apeld/bin/apeld-linux-arm64
var apeldLinuxARM64 []byte

//go:embed apeld/bin/apeld-darwin-arm64
var apeldDarwinARM64 []byte

// apeLoader is one embedded loader and the region of the APE header it
// occupies.
type apeLoader struct {
	name   string
	blob   []byte // the bytes as they sit in the APE header
	gzip   bool   // blob is gzipped, so the script pipes it through gzip -dc
	offset int    // where in the header those bytes go
	tag    string
}

// Loader regions of the 64K APE header.
const (
	apeLdLinuxAMD64Offset  = 0x2800
	apeLdLinuxARM64Offset  = 0x2c00
	apeLdDarwinARM64Offset = 0x8000
)

// apeLoaderFor returns the loader that boots p, or nil for a platform that
// needs none. windows/amd64 needs none: the file is a valid PE and the OS
// maps the payload straight from it.
func apeLoaderFor(p cosmoape.Platform) *apeLoader {
	switch p {
	case cosmoape.LinuxAMD64:
		return newApeLoader("apeld-linux-amd64", apeldLinuxAMD64, false, apeLdLinuxAMD64Offset)
	case cosmoape.LinuxARM64:
		return newApeLoader("apeld-linux-arm64", apeldLinuxARM64, false, apeLdLinuxARM64Offset)
	case cosmoape.DarwinARM64:
		return newApeLoader("apeld-darwin-arm64", apeldDarwinARM64, true, apeLdDarwinARM64Offset)
	}
	return nil
}

// newApeLoader tags a loader by the SHA-256 of the binary itself, never of
// the blob. The tag names the cache path an unpack writes to. It has to
// change when the loader changes and stay put when only the packing does.
func newApeLoader(name string, bin []byte, compress bool, offset int) *apeLoader {
	if len(bin) == 0 {
		Exitf("APE: the %s loader is empty; run make.bash (make.bat on Windows) in src, which compiles it with zig and LLVM", name)
	}
	sum := sha256.Sum256(bin)
	l := &apeLoader{name: name, blob: bin, gzip: compress, offset: offset, tag: hex.EncodeToString(sum[:4])}
	if compress {
		l.blob = apeGzip(bin, name)
	}
	return l
}

// apeGzip compresses bin at the fixed level every link uses, so links of
// the same input produce the same APE.
func apeGzip(bin []byte, name string) []byte {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		Exitf("APE: compressing the %s loader: %v", name, err)
	}
	if _, err := zw.Write(bin); err != nil {
		Exitf("APE: compressing the %s loader: %v", name, err)
	}
	if err := zw.Close(); err != nil {
		Exitf("APE: compressing the %s loader: %v", name, err)
	}
	return buf.Bytes()
}

// apeLoadersFor returns the loaders the selected platforms need, in header
// order. GOCOSMOAPELD names a directory of replacements, for a toolchain
// that has rebuilt them. A file in it whose name matches a loader's is used
// in place of the embedded copy.
func apeLoadersFor(plat cosmoape.Set) []*apeLoader {
	var out []*apeLoader
	for _, p := range plat.Platforms() {
		l := apeLoaderFor(p)
		if l == nil {
			continue
		}
		if dir := os.Getenv("GOCOSMOAPELD"); dir != "" {
			if bin, err := os.ReadFile(dir + "/" + l.name); err == nil {
				l = newApeLoader(l.name, bin, l.gzip, l.offset)
			}
		}
		out = append(out, l)
	}
	return out
}

// inApeLoader reports whether the header byte at off belongs to a loader.
func inApeLoader(loaders []*apeLoader, off int) bool {
	for _, l := range loaders {
		if off >= l.offset && off < l.offset+len(l.blob) {
			return true
		}
	}
	return false
}

// placeApeLoaders copies each loader into the APE header and ends the link
// on a region that runs past the header or into the next loader. A silent
// overlap would leave a loader that unpacks to garbage, and the failure
// would land on the host rather than on the build.
func placeApeLoaders(header []byte, loaders []*apeLoader) {
	end := 0
	for _, l := range loaders {
		if l.offset < end {
			Exitf("APE: the %s loader at %#x overlaps the region that ends at %#x", l.name, l.offset, end)
		}
		if l.offset+len(l.blob) > len(header) {
			Exitf("APE: the %s loader (%d bytes at %#x) runs past the %d-byte APE header", l.name, len(l.blob), l.offset, len(header))
		}
		copy(header[l.offset:], l.blob)
		end = l.offset + len(l.blob)
	}
}
