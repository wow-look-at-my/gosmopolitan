// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// A Linux host's kernel inotify, as a source of hints for the core. The calls
// go straight to the kernel, past the traps that serve callers.

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"internal/strconv"
	"runtime"
	"unsafe"
)

const (
	kernelOPath      = 0x200000
	kernelPollIn     = 0x1
	kernelMask       = IN_CLOSE_WRITE | IN_CREATE | IN_DELETE | IN_MOVED_FROM | IN_MOVED_TO | IN_MODIFY | IN_ATTRIB | IN_DELETE_SELF | IN_ONLYDIR
	kernelBufBytes   = 65536
	kernelBlockBytes = 4096
	kernelSeekData   = 3
	kernelSetLease   = 1024
	kernelProbeFlags = O_RDONLY | O_NONBLOCK | O_NOFOLLOW | O_CLOEXEC
)

type kernelPollFd struct {
	fd      int32
	events  int16
	revents int16
}

// A kernelName is a name in a directory.
type kernelName struct {
	dir  *inotifyDir
	name string
}

type kernelSource struct {
	ino   *inotifyInstance
	fd    int
	wds   map[int32]*inotifyDir
	sizes map[kernelName]int64 // the size last seen of a name with a write not yet reported
}

func kernelNewSource(ino *inotifyInstance) (inotifySource, Errno) {
	fd, _, errno := cosmo.Syscall6(SYS_INOTIFY_INIT1, IN_CLOEXEC, 0, 0, 0, 0, 0)
	if errno != 0 {
		return nil, Errno(errno)
	}
	return &kernelSource{ino: ino, fd: int(fd), wds: map[int32]*inotifyDir{}, sizes: map[kernelName]int64{}}, 0
}

// canonical asks the kernel where the file an O_PATH descriptor refers to is.
func (source *kernelSource) canonical(path string, follow bool) (string, Errno) {
	flags := kernelOPath | O_CLOEXEC
	if !follow {
		flags |= O_NOFOLLOW
	}
	fd, err := Open(path, flags, 0)
	if err != nil {
		return "", err.(Errno)
	}
	defer Close(fd)
	buf := make([]byte, 4096)
	count, err := Readlink("/proc/self/fd/"+strconv.Itoa(fd), buf)
	if err != nil {
		return "", err.(Errno)
	}
	return string(buf[:count]), 0
}

func (source *kernelSource) open(dir *inotifyDir) Errno {
	path, err := BytePtrFromString(dir.path)
	if err != nil {
		return err.(Errno)
	}
	wd, _, errno := cosmo.Syscall6(SYS_INOTIFY_ADD_WATCH, uintptr(source.fd), uintptr(unsafe.Pointer(path)), kernelMask, 0, 0, 0)
	runtime.KeepAlive(path)
	if errno != 0 {
		return Errno(errno)
	}
	dir.host = int32(wd)
	source.wds[int32(wd)] = dir
	return 0
}

func (source *kernelSource) close(dir *inotifyDir) {
	wd := dir.host.(int32)
	if source.wds[wd] != dir {
		return
	}
	delete(source.wds, wd)
	cosmo.Syscall6(SYS_INOTIFY_RM_WATCH, uintptr(source.fd), uintptr(wd), 0, 0, 0, 0)
}

func (source *kernelSource) moved(dir *inotifyDir, old string) {}

// stop has nothing to do: the poll in run wakes when the caller's end closes.
func (source *kernelSource) stop() {}

func (source *kernelSource) run(ino *inotifyInstance) {
	buf := make([]byte, kernelBufBytes)
	polled := [2]kernelPollFd{{fd: int32(source.fd), events: kernelPollIn}, {fd: int32(ino.writeFd), events: kernelPollIn}}
	for {
		polled[0].revents, polled[1].revents = 0, 0
		_, _, errno := Syscall6(SYS_PPOLL, uintptr(unsafe.Pointer(&polled)), 2, 0, 0, 0, 0)
		if errno == EINTR {
			continue
		}
		if errno != 0 || polled[1].revents != 0 {
			break
		}
		count, err := read(source.fd, buf)
		if err != nil || count <= 0 {
			break
		}
		ino.mu.Lock()
		source.records(buf[:count])
		ino.flush()
		ino.mu.Unlock()
	}
	<-ino.stopping
	Close(source.fd)
}

// records applies a read of kernel inotify_event records.
func (source *kernelSource) records(buf []byte) {
	for len(buf) >= SizeofInotifyEvent {
		event := (*InotifyEvent)(unsafe.Pointer(&buf[0]))
		end := SizeofInotifyEvent + int(event.Len)
		name := buf[SizeofInotifyEvent:end]
		for len(name) > 0 && name[len(name)-1] == 0 {
			name = name[:len(name)-1]
		}
		source.record(event.Wd, event.Mask, string(name))
		buf = buf[end:]
	}
}

func (source *kernelSource) record(wd int32, mask uint32, name string) {
	ino := source.ino
	if mask&IN_Q_OVERFLOW != 0 {
		for _, dir := range ino.paths {
			ino.resync(dir)
		}
		return
	}
	dir := source.wds[wd]
	if dir == nil || dir.closing {
		return
	}
	switch {
	case mask&(IN_DELETE_SELF|IN_UNMOUNT|IN_IGNORED) != 0:
		ino.lost(dir)
	case name == "":
		ino.selfChanged(dir)
	case mask&(IN_CREATE|IN_MOVED_TO) != 0:
		delete(source.sizes, kernelName{dir, name})
		ino.added(dir, name)
	case mask&IN_DELETE != 0:
		delete(source.sizes, kernelName{dir, name})
		ino.removed(dir, name, false)
	case mask&IN_MOVED_FROM != 0:
		delete(source.sizes, kernelName{dir, name})
		ino.removed(dir, name, true)
	case mask&IN_CLOSE_WRITE != 0:
		delete(source.sizes, kernelName{dir, name})
		ino.modifiedName(dir, name)
	case mask&IN_MODIFY != 0:
		source.modified(dir, name)
	case mask&IN_ATTRIB != 0:
		ino.modifiedName(dir, name)
	}
}

// modified handles IN_MODIFY, which means a write or a truncate. macOS and NT
// report a write when its descriptor closes and a truncate when it happens. A
// write always has a descriptor to close, which IN_CLOSE_WRITE reports.
func (source *kernelSource) modified(dir *inotifyDir, name string) {
	path := dir.join(name)
	truncated, unchanged := source.resized(dir, name, path)
	if truncated || !kernelWriterOpen(path, unchanged) {
		delete(source.sizes, kernelName{dir, name})
		source.ino.modifiedName(dir, name)
	}
}

// resized compares a file with its last size. truncated says the change can
// only be a truncate: the file shrank, or it grew by nothing a write leaves
// behind. unchanged says the size stayed the same.
func (source *kernelSource) resized(dir *inotifyDir, name, path string) (truncated, unchanged bool) {
	fresh := inotifyStat(path)
	if fresh == nil {
		return false, false
	}
	key := kernelName{dir, name}
	last, seen := source.sizes[key]
	if !seen {
		entry := dir.children[name]
		if watch := dir.selves[name]; entry == nil && watch != nil {
			entry = &watch.entry
		}
		if entry == nil {
			return false, false
		}
		last = entry.size
	}
	source.sizes[key] = fresh.size
	truncated = fresh.size < last || fresh.size > last && kernelNoDataPast(path, last, fresh.size)
	return truncated, fresh.size == last
}

// kernelNoDataPast reports whether a file holds zeros from an offset to the end
// of that block, and a hole after it up to its end.
func kernelNoDataPast(path string, from, end int64) bool {
	fd, err := Open(path, kernelProbeFlags, 0)
	if err != nil {
		return false
	}
	defer Close(fd)
	boundary := (from + kernelBlockBytes) &^ (kernelBlockBytes - 1)
	if boundary >= end {
		boundary = end
	} else if _, err := Seek(fd, boundary, kernelSeekData); err != ENXIO {
		return false
	}
	buf := make([]byte, boundary-from)
	count, err := pread(fd, buf, from)
	if err != nil || count != len(buf) {
		return false
	}
	for _, value := range buf {
		if value != 0 {
			return false
		}
	}
	return true
}

// kernelWriterOpen reports whether a change should wait for a descriptor to
// close. The kernel refuses a read lease while one has the file open for
// writing.
func kernelWriterOpen(path string, unchanged bool) bool {
	fd, err := Open(path, kernelProbeFlags, 0)
	if err != nil {
		return kernelLeaseWaits(err.(Errno), unchanged)
	}
	defer Close(fd)
	_, _, errno := cosmo.Syscall6(SYS_FCNTL, uintptr(fd), kernelSetLease, F_RDLCK, 0, 0, 0)
	if errno != 0 {
		return kernelLeaseWaits(Errno(errno), unchanged)
	}
	cosmo.Syscall6(SYS_FCNTL, uintptr(fd), kernelSetLease, F_UNLCK, 0, 0, 0)
	return false
}

// kernelLeaseWaits reads a refused lease. The kernel answers EACCES to anyone
// but the file's owner, which says nothing about writers.
func kernelLeaseWaits(errno Errno, unchanged bool) bool {
	return errno != EACCES || !unchanged
}
