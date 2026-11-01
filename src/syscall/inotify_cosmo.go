// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// inotify_cosmo.go holds the trap dispatch, the record format. The queue and the
// mask rules of the inotify calls this package serves on every host.

package syscall

import (
	"internal/bytealg"
	"unsafe"
)

const (
	// inMaskCreate is IN_MASK_CREATE, which Linux added after the syscall package's constants were frozen.
	inMaskCreate = 0x10000000

	inotifyKept = IN_ALL_EVENTS | IN_ONESHOT

	// inotifyMaskBits is Linux's ALL_INOTIFY_BITS. inotify_add_watch refuses
	// only a mask with none of them, so IN_ONESHOT alone is a valid mask.
	inotifyMaskBits = IN_ALL_EVENTS | IN_UNMOUNT | IN_Q_OVERFLOW | IN_IGNORED | IN_ONLYDIR | IN_DONT_FOLLOW |
		IN_EXCL_UNLINK | inMaskCreate | IN_MASK_ADD | IN_ISDIR | IN_ONESHOT

	// inotifyQueueMax is the default of Linux's max_queued_events.
	inotifyQueueMax = 16384
)

// A inotifyQueue holds the records the descriptor has no room for.
type inotifyQueue struct {
	records [][]byte
}

// inotifyRecord builds one struct inotify_event and its name, which Linux pads
// with NULs to a multiple of the record header.
func inotifyRecord(wd int32, mask, cookie uint32, name string) []byte {
	size := 0
	if name != "" {
		size = (len(name) + SizeofInotifyEvent) &^ (SizeofInotifyEvent - 1)
	}
	record := make([]byte, SizeofInotifyEvent+size)
	*(*InotifyEvent)(unsafe.Pointer(&record[0])) = InotifyEvent{Wd: wd, Mask: mask, Cookie: cookie, Len: uint32(size)}
	copy(record[SizeofInotifyEvent:], name)
	return record
}

// push queues a record. As on Linux, a record equal to the last one queued
// merges into it, and a full queue ends in one IN_Q_OVERFLOW.
func (queue *inotifyQueue) push(wd int32, mask, cookie uint32, name string) {
	record := inotifyRecord(wd, mask, cookie, name)
	if len(queue.records) >= inotifyQueueMax {
		record = inotifyRecord(-1, IN_Q_OVERFLOW, 0, "")
	}
	if last := len(queue.records) - 1; last >= 0 && string(queue.records[last]) == string(record) {
		return
	}
	queue.records = append(queue.records, record)
}

// An inotifyEntry is one name read from a directory.
type inotifyEntry struct {
	name string
	ino  uint64
	kind uint8
}

// inotifyTrap reports whether trap is an inotify syscall.
//
//go:nosplit
func inotifyTrap(trap uintptr) bool {
	switch trap {
	case SYS_INOTIFY_INIT1, SYS_INOTIFY_ADD_WATCH, SYS_INOTIFY_RM_WATCH, darwinSysInotifyInit:
		return true
	}
	return false
}

// inotifySyscall serves an inotify syscall. The path may point into the
// caller's stack and the emulation can grow that stack. It is turned into a
// pointer here, in a nosplit function.
//
//go:nosplit
func inotifySyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	switch trap {
	case darwinSysInotifyInit:
		return epollResult(inotifyInit(0))
	case SYS_INOTIFY_INIT1:
		return epollResult(inotifyInit(a1))
	case SYS_INOTIFY_ADD_WATCH:
		return epollResult(inotifyAddWatch(int(a1), (*byte)(unsafe.Pointer(a2)), uint32(a3)))
	case SYS_INOTIFY_RM_WATCH:
		return epollResult(0, inotifyRmWatch(int(a1), int32(a2)))
	}
	return ^uintptr(0), 0, ENOSYS
}

// inotifyPath copies a NUL-terminated path.
func inotifyPath(path *byte) string {
	length := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(path), length)) != 0 {
		length++
	}
	return string(unsafe.Slice(path, length))
}

// inotifyParse appends the Linux dirent records in buf to entries, leaving out
// the directory itself and its parent.
func inotifyParse(buf []byte, entries []inotifyEntry) []inotifyEntry {
	for len(buf) > 0 {
		reclen, ok := direntReclen(buf)
		if !ok || reclen == 0 || reclen > uint64(len(buf)) {
			break
		}
		record := buf[:reclen]
		buf = buf[reclen:]
		ino, ok := direntIno(record)
		if !ok || ino == 0 {
			continue
		}
		name := record[unsafe.Offsetof(Dirent{}.Name):]
		if end := bytealg.IndexByte(name, 0); end >= 0 {
			name = name[:end]
		}
		if text := string(name); text != "." && text != ".." {
			entries = append(entries, inotifyEntry{text, ino, record[unsafe.Offsetof(Dirent{}.Type)]})
		}
	}
	return entries
}

// inotifyMaskValid reports whether inotify_add_watch takes mask.
func inotifyMaskValid(mask uint32) bool {
	return mask&inotifyMaskBits != 0 && (mask&IN_MASK_ADD == 0 || mask&inMaskCreate == 0)
}

// inotifyMerged returns the mask of a watch added to again.
func inotifyMerged(current, mask uint32) uint32 {
	if mask&IN_MASK_ADD != 0 {
		return current | mask&inotifyKept
	}
	return mask & inotifyKept
}
