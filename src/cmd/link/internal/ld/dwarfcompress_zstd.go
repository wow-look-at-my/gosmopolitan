// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !compiler_bootstrap

package ld

import (
	"bytes"
	"compress/zlib"
	"debug/elf"
	"io"

	"cmd/internal/objabi"

	"github.com/klauspost/compress/zstd"
)

// dwarfCompressCodec returns the ELF compression header type used for the
// target's compressed DWARF sections: ELFCOMPRESS_ZSTD for GOOS=cosmo.
func dwarfCompressCodec(ctxt *Link) elf.CompressionType {
	if ctxt.HeadType == objabi.Hcosmo && ctxt.IsELF {
		return elf.COMPRESS_ZSTD
	}
	return elf.COMPRESS_ZLIB
}

// newDwarfCompressor returns a WriteCloser compressing into buf with the
// given codec (see dwarfCompressCodec).
func newDwarfCompressor(buf *bytes.Buffer, codec elf.CompressionType) (io.WriteCloser, error) {
	if codec == elf.COMPRESS_ZSTD {
		return zstd.NewWriter(buf,
			zstd.WithEncoderLevel(zstd.SpeedBestCompression),
			// One goroutine per encoder: output stays deterministic and dwarfcompress already runs one compressor per section.
			zstd.WithEncoderConcurrency(1))
	}
	// Using zlib.BestSpeed achieves nearly the same compression levels of zlib.DefaultCompression.
	return zlib.NewWriterLevel(buf, zlib.BestSpeed)
}
