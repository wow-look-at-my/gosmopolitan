// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package test

import (
	"bytes"
	"io/fs"
	"testing"
	"time"
)

type fakeInfo struct {
	size int64
	mode fs.FileMode
}

func (info fakeInfo) Name() string       { return "name" }
func (info fakeInfo) Size() int64        { return info.size }
func (info fakeInfo) Mode() fs.FileMode  { return info.mode }
func (info fakeInfo) ModTime() time.Time { return time.Time{} }
func (info fakeInfo) IsDir() bool        { return info.mode.IsDir() }
func (info fakeInfo) Sys() any           { return nil }

// TestStatDirSizeIsNoInput: APFS sizes a directory by its entries, so a stat
// of one must hash alike whatever it holds. A file's size stays an input.
func TestStatDirSizeIsNoInput(t *testing.T) {
	hashOf := func(info fs.FileInfo) string {
		var buf bytes.Buffer
		hashWriteStat(&buf, info)
		return buf.String()
	}
	if hashOf(fakeInfo{64, fs.ModeDir | 0o755}) != hashOf(fakeInfo{448, fs.ModeDir | 0o755}) {
		t.Error("a directory's size changed its stat hash")
	}
	if hashOf(fakeInfo{64, 0o644}) == hashOf(fakeInfo{448, 0o644}) {
		t.Error("a file's size left its stat hash alike")
	}
}
