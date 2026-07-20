// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build compiler_bootstrap

package ld

import (
	"bytes"
	"compress/zlib"
	"debug/elf"
	"io"
)

// The toolchain1 bootstrap build compiles cmd/link without the cmd/vendor
// tree, so the klauspost zstd encoder behind the GOOS=cosmo DWARF codec
// (see dwarfcompress_zstd.go) is unavailable and every target falls back
// to the upstream zlib path. That is sound: the bootstrap linker links
// only host bootstrap binaries during make.bash, and zlib-compressed
// debug sections stay valid for every reader even if it were pointed at
// a cosmo target. The real cmd/link installed by the build is compiled
// from the full module and uses zstd for cosmo.

func dwarfCompressCodec(ctxt *Link) elf.CompressionType {
	return elf.COMPRESS_ZLIB
}

func newDwarfCompressor(buf *bytes.Buffer, codec elf.CompressionType) (io.WriteCloser, error) {
	// Using zlib.BestSpeed achieves very nearly the same
	// compression levels of zlib.DefaultCompression, but takes
	// substantially less time. This is important because DWARF
	// compression can be a significant fraction of link time.
	return zlib.NewWriterLevel(buf, zlib.BestSpeed)
}
