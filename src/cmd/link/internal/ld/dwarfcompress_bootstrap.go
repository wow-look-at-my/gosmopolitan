// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build compiler_bootstrap

package ld

import (
	"bytes"
	"compress/zlib"
	"debug/elf"
	"io"
)

// The toolchain1 bootstrap build compiles cmd/link without the cmd/vendor
// tree.

func dwarfCompressCodec(ctxt *Link) elf.CompressionType {
	return elf.COMPRESS_ZLIB
}

func newDwarfCompressor(buf *bytes.Buffer, codec elf.CompressionType) (io.WriteCloser, error) {
	// Using zlib.BestSpeed achieves nearly the same compression levels of zlib.DefaultCompression.
	return zlib.NewWriterLevel(buf, zlib.BestSpeed)
}
