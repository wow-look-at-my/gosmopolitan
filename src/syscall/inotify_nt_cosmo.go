// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// An NT host's ReadDirectoryChangesW, as a source of hints for the core. One
// goroutine takes every directory's changes from a completion port.

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"unicode/utf16"
	"unsafe"
)

const (
	ntActionAdded      = 1
	ntActionRemoved    = 2
	ntActionModified   = 3
	ntActionRenamedOld = 4
	ntActionRenamedNew = 5

	ntErrorOperationAborted = 995
	ntErrorNotifyEnumDir    = 1022

	ntWaitForever = ^uint32(0)
	ntWatchBuffer = 65536
)

// An ntNotifyInformation is the head of Win32's FILE_NOTIFY_INFORMATION, which
// the name follows.
type ntNotifyInformation struct {
	next      uint32
	action    uint32
	nameBytes uint32
}

// An ntDirectory is one open directory and its read in flight.
type ntDirectory struct {
	dir    *inotifyDir
	key    uintptr
	handle uintptr

	buffer     []byte
	overlapped [4]uintptr
	armed      bool
}

type ntSource struct {
	ino         *inotifyInstance
	port        uintptr
	directories map[uintptr]*ntDirectory
	lastKey     uintptr
}

func ntNewSource(ino *inotifyInstance) (inotifySource, Errno) {
	port, failure := cosmo.Windows().WatchPort()
	if failure != 0 {
		return nil, Errno(failure)
	}
	return &ntSource{ino: ino, port: port, directories: map[uintptr]*ntDirectory{}}, 0
}

func (source *ntSource) canonical(path string, follow bool) (string, Errno) {
	full, failure := cosmo.Windows().FinalPath(path, follow)
	return full, Errno(failure)
}

func (source *ntSource) open(dir *inotifyDir) Errno {
	source.lastKey++
	handle, failure := cosmo.Windows().WatchOpen(dir.path, source.port, source.lastKey)
	if failure != 0 {
		return Errno(failure)
	}
	directory := &ntDirectory{dir: dir, key: source.lastKey, handle: handle, buffer: make([]byte, ntWatchBuffer)}
	dir.host = directory
	source.directories[directory.key] = directory
	if source.read(directory) != 0 {
		source.finish(directory)
		return EIO
	}
	return 0
}

// close stops using a directory. The kernel owns its buffer until the cancelled
// read's completion arrives, so that is when it closes.
func (source *ntSource) close(dir *inotifyDir) {
	directory := dir.host.(*ntDirectory)
	if directory.armed {
		cosmo.Windows().WatchCancel(directory.handle)
		return
	}
	source.finish(directory)
}

// moved has nothing to do: a handle stays on its directory through a rename.
func (source *ntSource) moved(dir *inotifyDir, old string) {}

func (source *ntSource) stop() {
	cosmo.Windows().WatchWake(source.port)
}

func (source *ntSource) finish(directory *ntDirectory) {
	cosmo.Windows().WatchClose(directory.handle)
	delete(source.directories, directory.key)
}

// read starts the directory's next read and reports a Win32 error.
func (source *ntSource) read(directory *ntDirectory) uintptr {
	directory.overlapped = [4]uintptr{}
	failure := cosmo.Windows().WatchRead(directory.handle, directory.buffer, &directory.overlapped)
	directory.armed = failure == 0
	return failure
}

func (source *ntSource) record(dir *inotifyDir, action uint32, name string) {
	ino := source.ino
	switch action {
	case ntActionAdded, ntActionRenamedNew:
		ino.added(dir, name)
	case ntActionRemoved, ntActionRenamedOld:
		ino.removed(dir, name, true)
	case ntActionModified:
		ino.modifiedName(dir, name)
	}
}

// records applies a FILE_NOTIFY_INFORMATION list.
func (source *ntSource) records(dir *inotifyDir, buf []byte) {
	const header = unsafe.Sizeof(ntNotifyInformation{})
	for uintptr(len(buf)) >= header && !dir.closing {
		info := (*ntNotifyInformation)(unsafe.Pointer(&buf[0]))
		end := header + uintptr(info.nameBytes)
		if end > uintptr(len(buf)) {
			return
		}
		var name string
		if info.nameBytes > 0 {
			units := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[header])), info.nameBytes/2)
			name = string(utf16.Decode(units))
		}
		source.record(dir, info.action, name)
		if info.next == 0 || uint64(info.next) > uint64(len(buf)) {
			return
		}
		buf = buf[info.next:]
	}
}

// complete takes a finished read and starts the next. A failed read is how a
// removed directory reports its removal. A read of nothing is a change list too
// long for the buffer, which NT drops whole.
func (source *ntSource) complete(key uintptr, transferred uint32, failure uintptr) {
	directory := source.directories[key]
	if directory == nil {
		return
	}
	directory.armed = false
	dir := directory.dir
	if dir.closing {
		source.finish(directory)
		return
	}
	switch {
	case failure == ntErrorOperationAborted:
	case failure != 0 && failure != ntErrorNotifyEnumDir:
		source.ino.lost(dir)
		return
	case transferred == 0:
		source.ino.resync(dir)
	default:
		source.records(dir, directory.buffer[:transferred])
	}
	if dir.closing {
		source.finish(directory)
		return
	}
	if source.read(directory) != 0 {
		source.ino.lost(dir)
	}
}

// run ends once the caller's end is closed and the last read has come back.
func (source *ntSource) run(ino *inotifyInstance) {
	fns := cosmo.Windows()
	for {
		transferred, key, completed, failure := fns.WatchWait(source.port, ntWaitForever)
		ino.mu.Lock()
		if completed {
			source.complete(key, transferred, failure)
		}
		ino.flush()
		finished := ino.closed && len(source.directories) == 0
		ino.mu.Unlock()
		if finished {
			break
		}
	}
	fns.WatchClose(source.port)
}
