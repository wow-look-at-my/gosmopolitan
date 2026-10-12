// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "unsafe"

// The Linux syscalls that name a path relative to the working directory. Each
// is its *at twin with AT_FDCWD.
const (
	ntSysOpen   = 2
	ntSysAccess = 21
	ntSysRename = 82
	ntSysMkdir  = 83
	ntSysRmdir  = 84
	ntSysUnlink = 87
)

// fcntl record-lock commands and lock types (Linux amd64).
const (
	ntFGetlk  = 5
	ntFSetlk  = 6
	ntFSetlkw = 7

	ntFRdlck = 0
	ntFWrlck = 1
	ntFUnlck = 2

	ntENOLCK = 37
)

// ntLinuxFlock is the Linux amd64 struct flock.
type ntLinuxFlock struct {
	ltype  int16
	whence int16
	_      int32
	start  int64
	length int64
	pid    int32
	_      int32
}

// ntLockSeg is one byte range this process holds, with the NT lock that backs
// it.
type ntLockSeg struct {
	start, end int64 // [start, end)
	write      bool
}

// ntLockFile is one file this process holds record locks on.
type ntLockFile struct {
	used     bool
	dev, ino uint64
	handle   uintptr
	segs     [ntLockSegMax]ntLockSeg
	nsegs    int
}

const (
	ntLockFileMax = 64
	ntLockSegMax  = 32
)

var (
	ntLockMu    mutex
	ntLockFiles [ntLockFileMax]ntLockFile
	ntLockUsed  int
)

// ntFileID reports the volume serial and file index of a handle, which
// is what fstat reports as st_dev and st_ino.
func ntFileID(handle uintptr) (dev, ino uint64, ok bool) {
	var info ntByHandleFileInformation
	if ret, _ := ntcallE(ntGetFileInformationByHandleFn, handle, uintptr(unsafe.Pointer(&info)), 0, 0, 0, 0, 0); ret == 0 {
		return 0, 0, false
	}
	return uint64(info.VolumeSerialNumber), uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), true
}

// ntLockRange takes or releases the NT lock on [start, end) of handle. It
// never waits, because ntLockMu is held: F_SETLKW sleeps in ntLockWait.
func ntLockRange(handle uintptr, start, end int64, take, write bool) bool {
	var over ntOverlapped
	over.offset = uint32(start)
	over.offsetHigh = uint32(start >> 32)
	size := uint64(end - start)
	if !take {
		ret, _ := ntcallE(ntUnlockFileExFn, handle, 0, uintptr(uint32(size)), uintptr(uint32(size>>32)),
			uintptr(unsafe.Pointer(&over)), 0, 0)
		return ret != 0
	}
	flags := uintptr(_NT_LOCKFILE_FAIL_IMMEDIATELY)
	if write {
		flags |= _NT_LOCKFILE_EXCLUSIVE_LOCK
	}
	ret, _ := ntcallE(ntLockFileExFn, handle, flags, 0, uintptr(uint32(size)), uintptr(uint32(size>>32)),
		uintptr(unsafe.Pointer(&over)), 0)
	return ret != 0
}

// ntEmuFcntlLock implements F_GETLK, F_SETLK and F_SETLKW.
func ntEmuFcntlLock(fd, cmd int32, flk *ntLinuxFlock) (r1, r2, errno uintptr) {
	entry, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if entry.kind != ntFDFile {
		return ntFail3(ntEINVAL)
	}
	if ntLockFileExFn == 0 || ntUnlockFileExFn == 0 {
		return ntFail3(ntENOSYS)
	}
	start := flk.start
	switch flk.whence {
	case 0: // SEEK_SET
	case 1: // SEEK_CUR
		pos, _, eno := ntEmuLseek(fd, 0, 1)
		if eno != 0 {
			return ntFail3(eno)
		}
		start += int64(pos)
	case 2: // SEEK_END
		pos, _, eno := ntEmuLseek(fd, 0, 2)
		if eno != 0 {
			return ntFail3(eno)
		}
		start += int64(pos)
	default:
		return ntFail3(ntEINVAL)
	}
	end := int64(1<<63 - 1)
	if flk.length > 0 {
		end = start + flk.length
	} else if flk.length < 0 {
		end = start
		start += flk.length
	}
	if start < 0 || end <= start {
		return ntFail3(ntEINVAL)
	}
	if flk.ltype != ntFRdlck && flk.ltype != ntFWrlck && flk.ltype != ntFUnlck {
		return ntFail3(ntEINVAL)
	}
	dev, ino, ok := ntFileID(entry.handle)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if cmd == ntFGetlk {
		return ntLockTest(entry.handle, dev, ino, flk, start, end)
	}
	for {
		eno := ntLockSet(entry.handle, dev, ino, flk.ltype, start, end)
		if eno != ntEAGAIN || cmd != ntFSetlkw || flk.ltype == ntFUnlck {
			if eno != 0 {
				return ntFail3(eno)
			}
			return 0, 0, 0
		}
		if eno := ntLockWait(entry.handle, dev, ino, flk.ltype == ntFWrlck, start, end); eno != 0 {
			return ntFail3(eno)
		}
		// A close of fd during the wait drops the request, as on Linux.
		if now, ok := ntFDLookup(fd); !ok || now.handle != entry.handle {
			return ntFail3(ntEBADF)
		}
	}
}

// ntLockWait is F_SETLKW's sleep, outside ntLockMu. It drops this process's
// own locks on [start, end) first, because NT holds a request against those
// too. So a conversion that has to wait is not atomic, like flock(2)'s. It
// then blocks in LockFileEx on a duplicate handle until no other holder
// conflicts, and lets go at once for ntLockSet to take the range.
func ntLockWait(handle uintptr, dev, ino uint64, write bool, start, end int64) uintptr {
	if eno := ntLockSet(handle, dev, ino, ntFUnlck, start, end); eno != 0 {
		return eno
	}
	probe, eno := ntDupHandle(handle)
	if eno != 0 {
		return eno
	}
	size := uint64(end - start)
	flags := uintptr(0)
	if write {
		flags = _NT_LOCKFILE_EXCLUSIVE_LOCK
	}
	var over ntOverlapped
	over.offset = uint32(start)
	over.offsetHigh = uint32(start >> 32)
	ret, werr := ntcallSE(ntLockFileExFn, probe, flags, 0, uintptr(uint32(size)), uintptr(uint32(size>>32)),
		uintptr(unsafe.Pointer(&over)), 0)
	if ret != 0 {
		var release ntOverlapped
		release.offset = uint32(start)
		release.offsetHigh = uint32(start >> 32)
		ntcallE(ntUnlockFileExFn, probe, 0, uintptr(uint32(size)), uintptr(uint32(size>>32)),
			uintptr(unsafe.Pointer(&release)), 0, 0)
	}
	ntcall(ntCloseHandleFn, probe, 0, 0, 0, 0, 0)
	if ret == 0 {
		return ntErrno(werr)
	}
	return 0
}

// ntLockFind returns the record of a file, creating it when create is set.
// ntLockMu must be held.
func ntLockFind(handle uintptr, dev, ino uint64, create bool) (*ntLockFile, uintptr) {
	free := -1
	for idx := range ntLockFiles {
		file := &ntLockFiles[idx]
		if file.used && file.dev == dev && file.ino == ino {
			return file, 0
		}
		if !file.used && free < 0 {
			free = idx
		}
	}
	if !create {
		return nil, 0
	}
	if free < 0 {
		return nil, ntENOLCK
	}
	dup, eno := ntDupHandle(handle)
	if eno != 0 {
		return nil, eno
	}
	file := &ntLockFiles[free]
	*file = ntLockFile{used: true, dev: dev, ino: ino, handle: dup}
	ntLockUsed++
	return file, 0
}

// ntLockSet changes this process's lock on [start, end) to ltype. Every
// segment the range touches is unlocked and the pieces are locked again,
// so a conflict restores the segments as they were.
func ntLockSet(handle uintptr, dev, ino uint64, ltype int16, start, end int64) uintptr {
	lock(&ntLockMu)
	defer unlock(&ntLockMu)
	file, eno := ntLockFind(handle, dev, ino, ltype != ntFUnlck)
	if eno != 0 {
		return eno
	}
	if file == nil {
		return 0
	}

	// The segments the range touches, and the pieces that replace them.
	var old, pieces [ntLockSegMax + 2]ntLockSeg
	nold, npieces := 0, 0
	var keep [ntLockSegMax]ntLockSeg
	nkeep := 0
	for _, seg := range file.segs[:file.nsegs] {
		if seg.end <= start || seg.start >= end {
			keep[nkeep] = seg
			nkeep++
			continue
		}
		old[nold] = seg
		nold++
		if seg.start < start {
			pieces[npieces] = ntLockSeg{start: seg.start, end: start, write: seg.write}
			npieces++
		}
		if seg.end > end {
			pieces[npieces] = ntLockSeg{start: end, end: seg.end, write: seg.write}
			npieces++
		}
	}
	if ltype != ntFUnlck {
		pieces[npieces] = ntLockSeg{start: start, end: end, write: ltype == ntFWrlck}
		npieces++
	}
	if nkeep+npieces > ntLockSegMax {
		return ntENOLCK
	}

	for _, seg := range old[:nold] {
		ntLockRange(file.handle, seg.start, seg.end, false, false)
	}
	for idx, seg := range pieces[:npieces] {
		if ntLockRange(file.handle, seg.start, seg.end, true, seg.write) {
			continue
		}
		// Another process holds part of it: put back what this held.
		for _, done := range pieces[:idx] {
			ntLockRange(file.handle, done.start, done.end, false, false)
		}
		for _, seg := range old[:nold] {
			if !ntLockRange(file.handle, seg.start, seg.end, true, seg.write) {
				print("runtime: NT lost a record lock it held on [", seg.start, ", ", seg.end, ")\n")
			}
		}
		return ntEAGAIN
	}

	copy(file.segs[:], keep[:nkeep])
	copy(file.segs[nkeep:], pieces[:npieces])
	file.nsegs = nkeep + npieces
	if file.nsegs == 0 {
		ntLockDrop(file)
	}
	return 0
}

// ntLockTest answers F_GETLK. Locks this process holds never conflict
// with it, so only the bytes it does not hold are tested.
func ntLockTest(handle uintptr, dev, ino uint64, flk *ntLinuxFlock, start, end int64) (r1, r2, errno uintptr) {
	lock(&ntLockMu)
	defer unlock(&ntLockMu)
	if flk.ltype == ntFUnlck {
		return 0, 0, 0
	}
	file, _ := ntLockFind(handle, dev, ino, false)
	probe := handle
	if file != nil {
		probe = file.handle
	}
	pos := start
	for pos < end {
		next := end
		held := false
		if file != nil {
			for _, seg := range file.segs[:file.nsegs] {
				if seg.start <= pos && pos < seg.end {
					held = true
					next = min(seg.end, end)
					break
				}
				if seg.start > pos && seg.start < next {
					next = seg.start
				}
			}
		}
		if !held {
			if !ntLockRange(probe, pos, next, true, flk.ltype == ntFWrlck) {
				flk.ltype = ntFWrlck
				flk.whence = 0
				flk.start = pos
				flk.length = next - pos
				flk.pid = -1
				return 0, 0, 0
			}
			ntLockRange(probe, pos, next, false, false)
		}
		pos = next
	}
	flk.ltype = ntFUnlck
	return 0, 0, 0
}

// ntLockDrop releases every lock of a file. ntLockMu must be held.
func ntLockDrop(file *ntLockFile) {
	for _, seg := range file.segs[:file.nsegs] {
		ntLockRange(file.handle, seg.start, seg.end, false, false)
	}
	ntcall(ntCloseHandleFn, file.handle, 0, 0, 0, 0, 0)
	*file = ntLockFile{}
	ntLockUsed--
}

// ntLockClose drops the record locks of the file behind handle, because on
// Linux closing any descriptor of a file releases the process's locks on it.
func ntLockClose(handle uintptr) {
	lock(&ntLockMu)
	used := ntLockUsed
	unlock(&ntLockMu)
	if used == 0 {
		return
	}
	dev, ino, ok := ntFileID(handle)
	if !ok {
		return
	}
	lock(&ntLockMu)
	if file, _ := ntLockFind(handle, dev, ino, false); file != nil {
		ntLockDrop(file)
	}
	unlock(&ntLockMu)
}
