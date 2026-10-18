// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"sync"
)

// pgzipBlock is the input size of one gzip member. A member starts with an
// empty dictionary, so a larger block loses less ratio at the start of each.
const pgzipBlock = 4 << 20

// parallelGzip writes a gzip stream as a sequence of members, one per block,
// compressed on every CPU. A gzip reader reads the members as one stream. The
// output depends only on the input bytes, so the archive stays reproducible.
type parallelGzip struct {
	out     io.Writer
	level   int
	buf     []byte
	pending chan chan pgzipResult
	done    chan error
	slots   chan struct{}
	bufs    sync.Pool
	blocks  int
}

type pgzipResult struct {
	data *bytes.Buffer
	err  error
}

func newParallelGzip(out io.Writer, level int) *parallelGzip {
	workers := runtime.GOMAXPROCS(0)
	pz := &parallelGzip{
		out:     out,
		level:   level,
		buf:     make([]byte, 0, pgzipBlock),
		pending: make(chan chan pgzipResult, 2*workers),
		done:    make(chan error, 1),
		slots:   make(chan struct{}, workers),
	}
	pz.bufs.New = func() any { return new(bytes.Buffer) }
	go pz.drain()
	return pz
}

// drain writes the members in input order. After the first error it only
// returns buffers, so Close reports that error.
func (pz *parallelGzip) drain() {
	var first error
	for result := range pz.pending {
		res := <-result
		if first == nil {
			first = res.err
		}
		if first == nil {
			_, first = pz.out.Write(res.data.Bytes())
		}
		res.data.Reset()
		pz.bufs.Put(res.data)
	}
	pz.done <- first
}

func (pz *parallelGzip) Write(data []byte) (int, error) {
	total := len(data)
	for len(data) > 0 {
		take := min(pgzipBlock-len(pz.buf), len(data))
		pz.buf = append(pz.buf, data[:take]...)
		data = data[take:]
		if len(pz.buf) == pgzipBlock {
			pz.flush()
		}
	}
	return total, nil
}

// flush hands the current block to a worker. It blocks while every worker is busy.
func (pz *parallelGzip) flush() {
	block := pz.buf
	pz.buf = make([]byte, 0, pgzipBlock)
	pz.blocks++
	result := make(chan pgzipResult, 1)
	pz.slots <- struct{}{}
	pz.pending <- result
	go func() {
		defer func() { <-pz.slots }()
		data := pz.bufs.Get().(*bytes.Buffer)
		zw, err := gzip.NewWriterLevel(data, pz.level)
		if err == nil {
			_, err = zw.Write(block)
		}
		if err == nil {
			err = zw.Close()
		}
		result <- pgzipResult{data: data, err: err}
	}()
}

// Close writes the last block. An empty stream still gets one member, so the
// file is a valid gzip file.
func (pz *parallelGzip) Close() error {
	if len(pz.buf) > 0 || pz.blocks == 0 {
		pz.flush()
	}
	close(pz.pending)
	return <-pz.done
}

// zipEntry is one file deflated ahead of the zip writer.
type zipEntry struct {
	data  []byte
	crc   uint32
	size  uint64
	err   error
	ready chan struct{}
}

// isLink reports whether name is a symlink. os.Readlink succeeds only for one.
func isLink(name string) bool {
	_, err := os.Readlink(name)
	return err == nil
}

// deflateFile compresses the whole of src at level and records its CRC-32 and size.
func deflateFile(src io.Reader, level int, entry *zipEntry) {
	defer close(entry.ready)
	var data bytes.Buffer
	fw, err := flate.NewWriter(&data, level)
	if err != nil {
		entry.err = err
		return
	}
	crc := crc32.NewIEEE()
	size, err := io.Copy(io.MultiWriter(fw, crc), src)
	if err == nil {
		err = fw.Close()
	}
	entry.data, entry.crc, entry.size, entry.err = data.Bytes(), crc.Sum32(), uint64(size), err
}
