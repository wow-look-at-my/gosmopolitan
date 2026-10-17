// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build js || wasip1

package filelock

import (
	"io/fs"
	"os"
	"sync"
)

// js and wasip1 have no advisory file lock, and a program there cannot start
// another process. These locks therefore exclude the goroutines of this
// process only. Like flock, they exclude separate opens of one file.

type lockType int8

const (
	readLock = iota + 1
	writeLock
)

// lockedFile is the lock state of one file, matched by os.SameFile.
type lockedFile struct {
	info    fs.FileInfo
	readers int
	writer  bool
}

// holding records which lock a File holds, so that unlock can release it.
type holding struct {
	file *lockedFile
	kind lockType
}

var (
	tableMu   sync.Mutex
	tableCond = sync.NewCond(&tableMu)
	locked    []*lockedFile
	holders   = map[File]holding{}
)

// lockedFileFor answers the entry for info, and adds one if none exists.
// The caller holds tableMu.
func lockedFileFor(info fs.FileInfo) *lockedFile {
	for _, entry := range locked {
		if os.SameFile(entry.info, info) {
			return entry
		}
	}
	entry := &lockedFile{info: info}
	locked = append(locked, entry)
	return entry
}

func lock(f File, lt lockType) error {
	info, err := f.Stat()
	if err != nil {
		return &fs.PathError{Op: lt.String(), Path: f.Name(), Err: err}
	}

	tableMu.Lock()
	defer tableMu.Unlock()
	entry := lockedFileFor(info)
	for entry.writer || (lt == writeLock && entry.readers > 0) {
		tableCond.Wait()
		// An unlock removes an entry that nothing holds, so look it up again.
		entry = lockedFileFor(info)
	}
	if lt == writeLock {
		entry.writer = true
	} else {
		entry.readers++
	}
	holders[f] = holding{entry, lt}
	return nil
}

func unlock(f File) error {
	tableMu.Lock()
	defer tableMu.Unlock()
	held, ok := holders[f]
	if !ok {
		return &fs.PathError{Op: "Unlock", Path: f.Name(), Err: fs.ErrInvalid}
	}
	delete(holders, f)
	if held.kind == writeLock {
		held.file.writer = false
	} else {
		held.file.readers--
	}
	if !held.file.writer && held.file.readers == 0 {
		for idx, entry := range locked {
			if entry == held.file {
				locked = append(locked[:idx], locked[idx+1:]...)
				break
			}
		}
	}
	tableCond.Broadcast()
	return nil
}
