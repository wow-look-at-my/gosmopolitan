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
	kernelHandle   = IN_OPEN | IN_CLOSE_WRITE | IN_CLOSE_NOWRITE
	kernelMask     = kernelHandle | IN_CREATE | IN_DELETE | IN_MOVED_FROM | IN_MOVED_TO | IN_MODIFY | IN_ATTRIB | IN_DELETE_SELF | IN_ONLYDIR
	kernelBufBytes = 65536
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
	opens map[kernelName]int
}

func kernelNewSource(ino *inotifyInstance) (inotifySource, Errno) {
	fd, _, errno := cosmo.Syscall6(SYS_INOTIFY_INIT1, IN_CLOEXEC, 0, 0, 0, 0, 0)
	if errno != 0 {
		return nil, Errno(errno)
	}
	return &kernelSource{ino: ino, fd: int(fd), wds: map[int32]*inotifyDir{}, opens: map[kernelName]int{}}, 0
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
	case mask&kernelHandle != 0:
		source.handle(dir, mask, name)
	case name == "":
		ino.selfChanged(dir)
	case mask&(IN_CREATE|IN_MOVED_TO) != 0:
		delete(source.opens, kernelName{dir, name})
		ino.added(dir, name)
	case mask&IN_DELETE != 0:
		delete(source.opens, kernelName{dir, name})
		ino.removed(dir, name, false)
	case mask&IN_MOVED_FROM != 0:
		delete(source.opens, kernelName{dir, name})
		ino.removed(dir, name, true)
	case mask&IN_MODIFY != 0:
		source.modified(dir, name)
	case mask&IN_ATTRIB != 0:
		ino.modifiedName(dir, name)
	}
}

// handle follows the descriptors open on a name. A closing descriptor is what
// reports a write.
func (source *kernelSource) handle(dir *inotifyDir, mask uint32, name string) {
	if name == "" || mask&IN_ISDIR != 0 {
		return
	}
	key := kernelName{dir, name}
	if mask&IN_OPEN != 0 {
		source.opens[key]++
		return
	}
	if source.opens[key] > 1 {
		source.opens[key]--
	} else {
		delete(source.opens, key)
	}
	source.ino.modifiedName(dir, name)
}

// modified handles IN_MODIFY, which means a write or a truncate. macOS and NT
// report a write when its descriptor closes and a truncate when it happens.
func (source *kernelSource) modified(dir *inotifyDir, name string) {
	if source.opens[kernelName{dir, name}] == 0 || source.shrank(dir, name) {
		source.ino.modifiedName(dir, name)
	}
}

func (source *kernelSource) shrank(dir *inotifyDir, name string) bool {
	entry := dir.children[name]
	if watch := dir.selves[name]; entry == nil && watch != nil {
		entry = &watch.entry
	}
	fresh := inotifyStat(dir.join(name))
	return entry != nil && fresh != nil && fresh.size < entry.size
}
