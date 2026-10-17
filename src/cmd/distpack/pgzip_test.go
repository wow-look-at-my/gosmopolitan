// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// pgzipInput is half random bytes and half repeated text, so both stored and
// compressed blocks occur.
func pgzipInput(size int) []byte {
	rng := rand.New(rand.NewPCG(1, uint64(size)))
	data := make([]byte, size)
	for idx := range data {
		if idx%2 == 0 {
			data[idx] = byte(rng.Uint32())
		} else {
			data[idx] = "distpack"[idx%8]
		}
	}
	return data
}

func pgzipCompress(t *testing.T, data []byte, chunk int) []byte {
	t.Helper()
	var out bytes.Buffer
	pz := newParallelGzip(&out, gzip.DefaultCompression)
	for rest := data; len(rest) > 0; {
		take := min(chunk, len(rest))
		if _, err := pz.Write(rest[:take]); err != nil {
			t.Fatal(err)
		}
		rest = rest[take:]
	}
	if err := pz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// A gzip reader reads every member back as the input, and two writes of the
// same input give the same bytes whatever the write sizes were.
func TestParallelGzipRoundTrip(t *testing.T) {
	for _, size := range []int{0, 1, pgzipBlock - 1, pgzipBlock, pgzipBlock + 1, 3*pgzipBlock + 5} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			data := pgzipInput(size)
			first := pgzipCompress(t, data, 1<<16)
			second := pgzipCompress(t, data, 12345)
			if !bytes.Equal(first, second) {
				t.Fatalf("the output depends on the write sizes")
			}
			zr, err := gzip.NewReader(bytes.NewReader(first))
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(zr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("read back %d bytes that differ from the %d written", len(got), len(data))
			}
		})
	}
}

// The zip deflates its files concurrently. Every file reads back whole and in
// archive order, and a symlink keeps its target as its content.
func TestWriteZipParallel(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var archive Archive
	var want []string
	for idx := range 40 {
		name := "file" + strconv.Itoa(idx)
		body := pgzipInput(idx * 9973)
		src := filepath.Join(dir, name)
		if err := os.WriteFile(src, body, 0o644); err != nil {
			t.Fatal(err)
		}
		archive.Files = append(archive.Files, File{Name: "go/" + name, Time: stamp, Mode: 0o644, Size: int64(len(body)), Src: src})
		want = append(want, string(body))
	}
	archive.Files = append(archive.Files, File{Name: "go/link", Time: stamp, Mode: 0o644, Src: filepath.Join(dir, "absent"), Linkname: "file0"})
	want = append(want, "file0")

	out := filepath.Join(dir, "out.zip")
	writeZip(out, &archive)
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != len(want) {
		t.Fatalf("the zip holds %d files, want %d", len(zr.File), len(want))
	}
	for idx, file := range zr.File {
		if file.Name != archive.Files[idx].Name {
			t.Fatalf("entry %d is %s, want %s", idx, file.Name, archive.Files[idx].Name)
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("%s: %v", file.Name, err)
		}
		if string(got) != want[idx] {
			t.Fatalf("%s reads back %d bytes that differ from the %d written", file.Name, len(got), len(want[idx]))
		}
	}
}
