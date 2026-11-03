// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// cmd/dist compiles the loaders into apeld/bin before it builds this package.

package ld

import (
	"bytes"
	"cmd/internal/cosmoape"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
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
	hash   string // the loader's full SHA-256, which keys the darwin cache
	// named boots through apeDarwinBootTmpl, which runs the loader from a cached copy with the program's file name.
	named bool
	bs    int // the dd block size
	low   int // the offset the caller asked for; a named loader can move past it
}

// apeLoaderBlock is the dd block size the script copies a loader with.
const apeLoaderBlock = 512

// block returns the dd block size for l.
func (l *apeLoader) block() int { return l.bs }

// Loader regions of the 64K APE header. The darwin loader takes the first
// offset at or past its constant that apeNamedPlacement allows.
const (
	apeLdLinuxAMD64Offset  = 0x2800
	apeLdLinuxARM64Offset  = 0x3800
	apeLdDarwinARM64Offset = 0x4800
)

// apeNamedPlacement places a loader that must unpack to its exact size.
// The dd block must divide both the size and the offset. So it picks the
// largest block that leaves the region inside the header. A small block
// makes dd do one read and one write per block.
func apeNamedPlacement(name string, size, low int) (offset, block int) {
	for blocks := 1; blocks <= size; blocks++ {
		if size%blocks != 0 {
			continue
		}
		block = size / blocks
		offset = (low + block - 1) / block * block
		if offset+size <= apeHeaderSize {
			return offset, block
		}
	}
	Exitf("APE: the %s loader (%d bytes) does not fit the APE header past %#x", name, size, low)
	return 0, 0
}

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
		// Uncompressed, so the unpack runs no gzip: each process is a fork and an exec on every launch with a cold cache.
		return newApeLoader("apeld-darwin-arm64", apeldDarwinARM64, false, apeLdDarwinARM64Offset)
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
	if offset%apeLoaderBlock != 0 {
		Exitf("APE: the %s loader region at %#x is not %d-aligned", name, offset, apeLoaderBlock)
	}
	sum := sha256.Sum256(bin)
	l := &apeLoader{name: name, gzip: compress, offset: offset, tag: hex.EncodeToString(sum[:4]), hash: hex.EncodeToString(sum[:]), bs: apeLoaderBlock, low: offset}
	// The darwin loader runs from a cache named by its hash, so its unpacked copy must be its exact bytes.
	l.named = name == "apeld-darwin-arm64"
	switch {
	case compress:
		l.blob = apeGzip(bin, name)
	case l.named:
		l.blob = bin
		l.offset, l.bs = apeNamedPlacement(name, len(bin), offset)
	default:
		// Bytes past the end of an ELF file are never mapped.
		l.blob = make([]byte, apeBlockCeil(len(bin)))
		copy(l.blob, bin)
	}
	return l
}

func apeBlockCeil(n int) int {
	return (n + apeLoaderBlock - 1) / apeLoaderBlock * apeLoaderBlock
}

// apeGzip compresses bin at the fixed level every link uses, so links of
// the same input produce the same APE. A header extra field pads the stream to
// whole blocks. A gzip can refuse bytes after the stream, and every gzip
// skips the extra field. A comment cannot pad, because Go's reader refuses a long one.
func apeGzip(bin []byte, name string) []byte {
	stream := apeGzipWith(bin, name, nil)
	if len(stream)%apeLoaderBlock == 0 {
		return stream
	}
	// The extra field adds its 2-byte length and then itself: a subfield ID, a 2-byte length and the zeros.
	size := apeBlockCeil(len(stream)+6) - len(stream) - 2
	extra := make([]byte, size)
	extra[0], extra[1] = 'A', 'P'
	binary.LittleEndian.PutUint16(extra[2:], uint16(size-4))
	return apeGzipWith(bin, name, extra)
}

func apeGzipWith(bin []byte, name string, extra []byte) []byte {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		Exitf("APE: compressing the %s loader: %v", name, err)
	}
	zw.Extra = extra
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
				l = newApeLoader(l.name, bin, l.gzip, l.low)
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
