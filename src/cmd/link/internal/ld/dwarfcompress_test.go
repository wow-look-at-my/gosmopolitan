// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"internal/testenv"
	"internal/zstd"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cmd/internal/objabi"
)

// TestAPEDwarfCompressCodec verifies the debug-section codec choice:
// zstd for GOOS=cosmo ELF output, the upstream zlib everywhere else.
func TestAPEDwarfCompressCodec(t *testing.T) {
	cosmo := &Link{Target: Target{HeadType: objabi.Hcosmo, IsELF: true}}
	if got := dwarfCompressCodec(cosmo); got != elf.COMPRESS_ZSTD {
		t.Errorf("cosmo codec = %v, want COMPRESS_ZSTD", got)
	}
	for _, tt := range []struct {
		head  objabi.HeadType
		isELF bool
	}{
		{objabi.Hlinux, true},
		{objabi.Hfreebsd, true},
		{objabi.Hdarwin, false},
		{objabi.Hwindows, false},
	} {
		ctxt := &Link{Target: Target{HeadType: tt.head, IsELF: tt.isELF}}
		if got := dwarfCompressCodec(ctxt); got != elf.COMPRESS_ZLIB {
			t.Errorf("HeadType %v codec = %v, want COMPRESS_ZLIB", tt.head, got)
		}
	}
}

// TestAPEDwarfZstdRoundTrip verifies the zstd compressor produces
// deterministic frames that the standard library decoder - the one
// debug/elf uses for ELFCOMPRESS_ZSTD sections - decompresses back to
// the input.
func TestAPEDwarfZstdRoundTrip(t *testing.T) {
	var in bytes.Buffer
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&in, "DW_TAG_subprogram %d DW_AT_name main.f%d\x00", i, i%97)
	}

	compress := func() []byte {
		var buf bytes.Buffer
		z, err := newDwarfCompressor(&buf, elf.COMPRESS_ZSTD)
		if err != nil {
			t.Fatalf("newDwarfCompressor: %v", err)
		}
		if _, err := z.Write(in.Bytes()); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := z.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		return buf.Bytes()
	}

	c1 := compress()
	if !bytes.Equal(c1, compress()) {
		t.Errorf("zstd compression is not deterministic (reproducible builds require it)")
	}
	if len(c1) >= in.Len() {
		t.Errorf("compressed %d bytes to %d, want smaller", in.Len(), len(c1))
	}
	out, err := io.ReadAll(zstd.NewReader(bytes.NewReader(c1)))
	if err != nil {
		t.Fatalf("internal/zstd (debug/elf's decoder) rejects the stream: %v", err)
	}
	if !bytes.Equal(out, in.Bytes()) {
		t.Errorf("round trip mismatch: got %d bytes, want %d", len(out), in.Len())
	}
}

// checkDebugSectionCodec asserts every compressed .debug_* section of the
// ELF file at path carries the wanted compression header type and that
// debug/elf can decompress it.
func checkDebugSectionCodec(t *testing.T, path string, want elf.CompressionType) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer f.Close()

	compressed := 0
	for _, s := range f.Sections {
		if !strings.HasPrefix(s.Name, ".debug_") {
			continue
		}
		if s.Flags&elf.SHF_COMPRESSED == 0 {
			// Sections whose compression would not shrink them are
			// stored raw (compressSyms returns nil).
			continue
		}
		compressed++
		got := elf.CompressionType(binary.LittleEndian.Uint32(raw[s.Offset : s.Offset+4]))
		if got != want {
			t.Errorf("%s: %s Chdr type = %v, want %v", filepath.Base(path), s.Name, got, want)
		}
		data, err := s.Data()
		if err != nil {
			t.Errorf("%s: %s: debug/elf decompression failed: %v", filepath.Base(path), s.Name, err)
		} else if len(data) == 0 {
			t.Errorf("%s: %s: decompressed to 0 bytes", filepath.Base(path), s.Name)
		}
	}
	if compressed == 0 {
		t.Errorf("%s has no compressed .debug_* sections", filepath.Base(path))
	}
}

// TestAPECosmoZstdDebugSidecars builds a real GOOS=cosmo fat APE and
// verifies both per-architecture debug sidecars carry zstd-compressed
// (ELFCOMPRESS_ZSTD) .debug_* sections readable by debug/elf - and that
// a GOOS=linux build from the same toolchain still uses zlib, pinning
// the upstream codec path for non-cosmo targets.
func TestAPECosmoZstdDebugSidecars(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	if testing.Short() {
		t.Skip("builds cosmo std for two architectures in short mode")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module zstdprobe\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	const prog = `package main

import "fmt"

func main() {
	greeting := "zstd"
	fmt.Println(greeting, 42)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(prog), 0644); err != nil {
		t.Fatal(err)
	}

	build := func(out string, env ...string) {
		t.Helper()
		cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", out, ".")
		cmd.Dir = dir
		// Pin the knobs so ambient GOCOSMO* settings cannot change
		// which sidecars exist or what they carry.
		cmd.Env = append(os.Environ(),
			"GOCOSMOFAT=", "GOCOSMOFAT_INNER=", "GOCOSMOSTRIP=", "GOCOSMODEBUG=")
		cmd.Env = append(cmd.Env, env...)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build -o %s: %v\n%s", out, err, msg)
		}
	}

	ape := filepath.Join(dir, "prog.com")
	build(ape, "GOOS=cosmo", "GOARCH=amd64")
	checkDebugSectionCodec(t, ape+".dbg", elf.COMPRESS_ZSTD)
	checkDebugSectionCodec(t, ape+".aarch64.elf", elf.COMPRESS_ZSTD)

	host := filepath.Join(dir, "prog.linux")
	build(host, "GOOS=linux", "GOARCH=amd64")
	checkDebugSectionCodec(t, host, elf.COMPRESS_ZLIB)
}
