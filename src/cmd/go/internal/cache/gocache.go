// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cache is the go command's view of the build cache.
//
// The cache itself is go-s3-server's: cachedisk is the directory on disk, and
// cacheclient puts the store and the broker over it. This package names those
// types for the go command. It adds the action hash, whose meaning is the go
// command's, and the one read a cache cannot do for it, which is a mapped one.
package cache

import (
	"fmt"
	"internal/godebug"
	"io"

	"cmd/go/internal/mmap"

	"github.com/wow-look-at-my/go-s3-server/cacheclient/cachedisk"
)

// An ActionID is a cache action key, the hash of a complete description of a
// repeatable computation: the command line, the environment, the input file
// contents and the executable contents.
type ActionID = cachedisk.ActionID

// An OutputID is a cache output key, the hash of an output of a computation.
type OutputID = cachedisk.OutputID

// A Cache is what the go command stores build artifacts in.
type Cache = cachedisk.Cache

// An Entry is what the cache holds for an action.
type Entry = cachedisk.Entry

// DebugTest makes the test cache say why it reused a result or did not. The
// decision is the go command's rather than the cache's, so the knob is read
// here.
var DebugTest = false

var gocachetest = godebug.New("gocachetest")

func init() {
	if gocachetest.Value() == "1" {
		gocachetest.IncNonDefault()
		DebugTest = true
	}
}

// GetFile looks up an action and answers the file holding its output.
func GetFile(c Cache, id ActionID) (string, Entry, error) {
	return cachedisk.GetFile(c, id)
}

// GetBytes looks up an action and answers its output. Use it only for output
// that fits in memory.
func GetBytes(c Cache, id ActionID) ([]byte, Entry, error) {
	return cachedisk.GetBytes(c, id)
}

// PutBytes stores bytes as an action's output.
func PutBytes(c Cache, id ActionID, data []byte) error {
	return cachedisk.PutBytes(c, id, data)
}

// PutNoVerify stores an output that is not reproducible, such as test output,
// so GODEBUG=goverifycache=1 does not hold it to a second identical run.
func PutNoVerify(c Cache, id ActionID, file io.ReadSeeker) (OutputID, int64, error) {
	return cachedisk.PutNoVerify(c, id, file)
}

// GetMmap looks up an action and maps its output rather than reading it. Use
// it only for output that fits in memory.
//
// The boolean reports whether the file was opened. While it is open the caller
// must not write to it on Windows, which locks an open file. The mapping lives
// here rather than in the cache, because how a file is mapped belongs to the
// program doing the mapping.
func GetMmap(c Cache, id ActionID) ([]byte, Entry, bool, error) {
	entry, err := c.Get(id)
	if err != nil {
		return nil, entry, false, err
	}
	md, opened, err := mmap.Mmap(c.OutputFile(entry.OutputID))
	if err != nil {
		return nil, Entry{}, opened, err
	}
	if int64(len(md.Data)) != entry.Size {
		return nil, Entry{}, true, fmt.Errorf("cache: %x holds %d bytes, not the %d it records", entry.OutputID, len(md.Data), entry.Size)
	}
	return md.Data, entry, true, nil
}
