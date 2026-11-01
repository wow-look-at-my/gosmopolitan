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
	kernelOPath    = 0x200000
	kernelPollIn   = 0x1
	kernelMask     = IN_CREATE | IN_DELETE | IN_MOVED_FROM | IN_MOVED_TO | IN_MODIFY | IN_ATTRIB | IN_DELETE_SELF | IN_ONLYDIR
	kernelBufBytes = 65536
)

type kernelPollFd struct {
	fd      int32
	events  int16
	revents int16
}

type kernelSource struct {
	ino *inotifyInstance
	fd  int
	wds map[int32]*inotifyDir
}

func kernelNewSource(ino *inotifyInstance) (inotifySource, Errno) {
	fd, _, errno := cosmo.Syscall6(SYS_INOTIFY_INIT1, IN_CLOEXEC, 0, 0, 0, 0, 0)
	if errno != 0 {
		return nil, Errno(errno)
	}
	return &kernelSource{ino: ino, fd: int(fd), wds: map[int32]*inotifyDir{}}, 0
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
		ino.added(dir, name)
	case mask&IN_DELETE != 0:
		ino.removed(dir, name, false)
	case mask&IN_MOVED_FROM != 0:
		ino.removed(dir, name, true)
	case mask&(IN_MODIFY|IN_ATTRIB) != 0:
		ino.modifiedName(dir, name)
	}
}
