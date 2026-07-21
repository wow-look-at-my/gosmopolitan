// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

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
// target's compressed DWARF sections: ELFCOMPRESS_ZSTD for GOOS=cosmo,
// ELFCOMPRESS_ZLIB everywhere else.
//
// Cosmo uses zstd because its debug sections dominate the on-disk cost of
// the per-architecture sidecars every fat APE build writes: klauspost zstd
// at SpeedBestCompression stores the same DWARF in 13-16% fewer bytes than
// the zlib BestSpeed default, for about +0.2s per link. Consumers need
// zstd-aware ELF tooling (gdb >= 13, binutils >= 2.40, Go's debug/elf
// since 1.21; delve reads it too). Non-cosmo targets keep the upstream
// zlib path byte-for-byte.
//
// The vendored zstd encoder is not available to the toolchain1 bootstrap
// build; dwarfcompress_bootstrap.go supplies a zlib-only fallback there
// (the bootstrap linker only links host bootstrap binaries, and zlib
// output remains valid for every reader in any case).
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
			// One goroutine per encoder: output stays deterministic
			// and dwarfcompress already runs one compressor per
			// section in parallel.
			zstd.WithEncoderConcurrency(1))
	}
	// Using zlib.BestSpeed achieves very nearly the same
	// compression levels of zlib.DefaultCompression, but takes
	// substantially less time. This is important because DWARF
	// compression can be a significant fraction of link time.
	return zlib.NewWriterLevel(buf, zlib.BestSpeed)
}
