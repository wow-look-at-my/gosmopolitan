// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// A macOS host's FSEvents, as a source of hints for the core. A kqueue watch
// costs a descriptor for each file in a directory, and FSEvents costs none.
//
// A stream's flags stick to a path and say what has happened to it lately, not
// what this event is. Only a stat says what an event means. A stream covers
// everything under its directory, and an event finds its directory by name.
// Streams over one file report one event with one id.
//
// Events go from a C callback through a pipe to a goroutine. That goroutine
// takes no lock the owner of a stream holds. Stopping a stream can never wait
// for a callback stuck on a full pipe.

package syscall

import (
	"internal/bytealg"
	"internal/byteorder"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	fseventMustScan     = 0x1
	fseventUserDropped   = 0x2
	fseventKernelDropped = 0x4
	fseventRemoved       = 0x200
	fseventRenamed       = 0x800

	// fseventHeaderBytes is the id, flags and path length cosmo_fsevents_callback writes.
	fseventHeaderBytes = 16

	fseventSeenMax = 8192

	fseventPathMax = 1024
)

func cosmoFSEventInit() bool

func cosmoFSEventQueue() uintptr

func cosmoFSEventDrain(queue uintptr)

func cosmoFSEventOpen(path *byte, fd int32, queue uintptr) uintptr

func cosmoFSEventClose(stream uintptr)

func cosmoFSEventPath(path *byte, follow bool, out *byte) bool

var fseventLoaded struct {
	once sync.Once
	ok   bool
}

type fseventSource struct {
	ino     *inotifyInstance
	queue   uintptr
	readFd  int
	writeFd int
	seen    map[uint64]struct{}

	mu      sync.Mutex
	arrived sync.Cond
	pending []byte
	ended   bool
}

func fseventNewSource(ino *inotifyInstance) (inotifySource, Errno) {
	fseventLoaded.once.Do(func() { fseventLoaded.ok = cosmoFSEventInit() })
	if !fseventLoaded.ok {
		return nil, ENOSYS
	}
	var pair [2]int
	if err := Pipe2(pair[:], O_CLOEXEC); err != nil {
		return nil, err.(Errno)
	}
	source := &fseventSource{
		ino:     ino,
		queue:   cosmoFSEventQueue(),
		readFd:  pair[0],
		writeFd: pair[1],
		seen:    map[uint64]struct{}{},
	}
	source.arrived.L = &source.mu
	return source, 0
}

// canonical takes its answer from the file's open handle, so a directory is
// named the way FSEvents will name it. The stat is what gives an error its errno.
func (source *fseventSource) canonical(path string, follow bool) (string, Errno) {
	var stat Stat_t
	var err error
	if follow {
		err = Stat(path, &stat)
	} else {
		err = Lstat(path, &stat)
	}
	if err != nil {
		return "", err.(Errno)
	}
	text, err := BytePtrFromString(path)
	if err != nil {
		return "", err.(Errno)
	}
	var out [fseventPathMax]byte
	if !cosmoFSEventPath(text, follow, &out[0]) {
		return "", EIO
	}
	return string(out[:bytealg.IndexByte(out[:], 0)]), 0
}

func (source *fseventSource) open(dir *inotifyDir) Errno {
	source.ino.probeHere("open start")
	path, err := BytePtrFromString(dir.path)
	if err != nil {
		return err.(Errno)
	}
	source.ino.probeHere("open bytes")
	var stream uintptr
	if mode, _ := Getenv("RP_FSE"); mode == "noop" {
		stream = 1<<33 + 0x5a5a5a
	} else {
		stream = cosmoFSEventOpen(path, int32(source.writeFd), source.queue)
	}
	if stream == 0 {
		return EIO
	}
	source.ino.probeHere("open c")
	dir.host = stream
	dir.probeStream = stream
	dir.probeBox = (*[2]uintptr)(unsafe.Pointer(&dir.host))[1]
	source.ino.probeHere("open host")
	probeKeep(dir)
	probeStop(dir)
	return 0
}

var probeStopped atomic.Bool

var (
	probeKeepMu  sync.Mutex
	probeKeepDir []*inotifyDir
	probeKeepBox []unsafe.Pointer
)

func probeKeep(dir *inotifyDir) {
	mode := getenvProbe()
	if mode == 0 {
		return
	}
	probeKeepMu.Lock()
	defer probeKeepMu.Unlock()
	if mode == 1 {
		probeKeepDir = append(probeKeepDir, dir)
		return
	}
	probeKeepBox = append(probeKeepBox, (*[2]unsafe.Pointer)(unsafe.Pointer(&dir.host))[1])
}

func getenvProbe() int {
	text, _ := Getenv("RP_KEEP")
	switch text {
	case "dir":
		return 1
	case "box":
		return 2
	}
	return 0
}

func probeDigits(value uintptr) string {
	var out [20]byte
	idx := len(out)
	for {
		idx--
		out[idx] = byte('0' + value%10)
		value /= 10
		if value == 0 {
			return string(out[idx:])
		}
	}
}

// probeStop parks a preempt-check worker until lldb has armed a watchpoint.
func probeStop(dir *inotifyDir) {
	if len(dir.path) < 30 || dir.path[13:30] != "rp-inotify-preemp" || probeStopped.Swap(true) {
		return
	}
	text := "process handle SIGURG -n false -p true -s false\n" +
		"process handle SIGPIPE -n false -p true -s false\n" +
		"watchpoint set expression -w write -s 8 -- " + probeDigits(dir.probeBox) + "\n" +
		"platform shell touch /private/tmp/rp-go\n" +
		"continue\nregister read\nthread backtrace all\nkill\nquit\n"
	fd, _ := Open("/private/tmp/rp-lldb.tmp", O_CREAT|O_WRONLY|O_TRUNC, 0644)
	Write(fd, []byte(text))
	Close(fd)
	fd, _ = Open("/private/tmp/rp-pid", O_CREAT|O_WRONLY|O_TRUNC, 0644)
	Write(fd, []byte(probeDigits(uintptr(Getpid()))))
	Close(fd)
	Rename("/private/tmp/rp-lldb.tmp", "/private/tmp/rp-lldb")
	var stat Stat_t
	for Stat("/private/tmp/rp-go", &stat) != nil {
	}
}

func (source *fseventSource) close(dir *inotifyDir) {
	dir.probe("close")
	if dir.probeStream == 1<<33+0x5a5a5a {
		return
	}
	cosmoFSEventClose(dir.probeStream)
}

// moved follows a directory to its new path. A stream watches a path, so what
// happened since the rename is only in the directory itself.
func (source *fseventSource) moved(dir *inotifyDir, old string) {
	source.close(dir)
	if source.open(dir) != 0 {
		dir.host = uintptr(0)
		source.ino.lost(dir)
		return
	}
	source.ino.resync(dir)
}

// stop runs after every stream has stopped. The pipe closes when the last
// callback has finished, which ends pump and then run.
func (source *fseventSource) stop() {
	cosmoFSEventDrain(source.queue)
	Close(source.writeFd)
}

// pump keeps the pipe empty.
func (source *fseventSource) pump() {
	buf := make([]byte, 65536)
	for {
		count, err := read(source.readFd, buf)
		if err == EINTR {
			continue
		}
		if err != nil || count <= 0 {
			break
		}
		source.mu.Lock()
		source.pending = append(source.pending, buf[:count]...)
		source.arrived.Signal()
		source.mu.Unlock()
	}
	Close(source.readFd)
	source.mu.Lock()
	source.ended = true
	source.arrived.Signal()
	source.mu.Unlock()
}

// take waits for what pump has read.
func (source *fseventSource) take() (batch []byte, ended bool) {
	source.mu.Lock()
	defer source.mu.Unlock()
	for len(source.pending) == 0 && !source.ended {
		source.arrived.Wait()
	}
	batch, source.pending = source.pending, nil
	return batch, source.ended
}

func (source *fseventSource) run(ino *inotifyInstance) {
	go source.pump()
	var carry []byte
	for {
		batch, ended := source.take()
		carry = append(carry, batch...)
		ino.mu.Lock()
		carry = source.events(carry)
		ino.flush()
		ino.mu.Unlock()
		if ended {
			return
		}
	}
}

// events applies the whole events in buf and returns what follows them.
func (source *fseventSource) events(buf []byte) []byte {
	for len(buf) >= fseventHeaderBytes {
		id := byteorder.LEUint64(buf)
		flags := byteorder.LEUint32(buf[8:])
		end := fseventHeaderBytes + int(byteorder.LEUint32(buf[12:]))
		if len(buf) < end {
			break
		}
		if source.first(id) {
			source.event(string(buf[fseventHeaderBytes:end]), flags)
		}
		buf = buf[end:]
	}
	return append([]byte(nil), buf...)
}

// first reports whether no stream has delivered the event yet.
func (source *fseventSource) first(id uint64) bool {
	if _, again := source.seen[id]; again {
		return false
	}
	if len(source.seen) >= fseventSeenMax {
		clear(source.seen)
	}
	source.seen[id] = struct{}{}
	return true
}

func (source *fseventSource) event(path string, flags uint32) {
	ino := source.ino
	if flags&(fseventMustScan|fseventUserDropped|fseventKernelDropped) != 0 {
		for _, dir := range ino.paths {
			ino.resync(dir)
		}
		return
	}
	if dir := ino.paths[path]; dir != nil {
		ino.selfChanged(dir)
	}
	slash := bytealg.LastIndexByteString(path, '/')
	if slash < 0 || slash == len(path)-1 {
		return
	}
	above := path[:slash]
	if slash == 0 {
		above = "/"
	}
	if dir := ino.paths[above]; dir != nil && !dir.closing {
		source.name(dir, path[slash+1:], flags)
	}
}

// name decides what an event about a name in a directory means.
func (source *fseventSource) name(dir *inotifyDir, name string, flags uint32) {
	ino := source.ino
	known, watch := dir.children[name], dir.selves[name]
	fresh := inotifyStat(dir.join(name))
	switch {
	case fresh == nil && flags&(fseventRemoved|fseventRenamed) == 0:
		// A name that has gone gets its answer from the event that carries the leaving.
	case fresh == nil && (known != nil || watch != nil):
		ino.removed(dir, name, flags&fseventRenamed != 0)
	case fresh == nil:
		source.vanished(dir, name)
	case known != nil && known.id == fresh.id, known == nil && watch != nil && watch.entry.id == fresh.id:
		ino.modifiedName(dir, name)
	default:
		ino.added(dir, name)
	}
}

// vanished handles the leaving of a name that was never seen, whose whole life
// fell between looks at the directory.
func (source *fseventSource) vanished(dir *inotifyDir, name string) {
	ino := source.ino
	if dir.children == nil || ino.heldNamed(dir, name) != nil {
		return
	}
	ino.added(dir, name)
	ino.removed(dir, name, false)
}
