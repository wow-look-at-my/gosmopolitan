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
	"internal/strconv"
	"sync"
	"sync/atomic"
)

const (
	fseventMustScan      = 0x1
	fseventUserDropped   = 0x2
	fseventKernelDropped = 0x4
	fseventRemoved       = 0x200
	fseventRenamed       = 0x800

	fseventDropped = fseventMustScan | fseventUserDropped | fseventKernelDropped

	// fseventCookieName starts the name of every instance's cookies, which no watch of the temporary directory reports.
	fseventCookieName = ".inotify-fsevents-"

	// fseventHeaderBytes is the id, flags and path length cosmo_fsevents_callback writes.
	fseventHeaderBytes = 16

	fseventSeenMax = 8192

	fseventPathMax = 1024
)

func cosmoFSEventInit() bool

func cosmoFSEventQueue() uintptr

func cosmoFSEventDrain(queue uintptr)

func cosmoFSEventOpen(path, cookie *byte, fd int32, queue uintptr) uintptr

func cosmoFSEventClose(stream uintptr)

func cosmoFSEventPath(path *byte, follow bool, out *byte) bool

var fseventLoaded struct {
	once sync.Once
	ok   bool
}

var fseventCookies atomic.Uint64

// An fseventStream is a directory's stream.
type fseventStream struct {
	stream uintptr
	cookie string
	since  uint64
	closed bool
}

// An fseventRecord is an event waiting for a cookie.
type fseventRecord struct {
	id    uint64
	flags uint32
	path  string
}

type fseventSource struct {
	ino     *inotifyInstance
	queue   uintptr
	readFd  int
	writeFd int
	seen    map[uint64]struct{}
	cookies string
	marks   map[string]*fseventStream
	held    []fseventRecord

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
	temp, found := Getenv("TMPDIR")
	if !found || temp == "" {
		temp = "/tmp"
	}
	cookies, errno := fseventCanonical(temp, true)
	if errno != 0 {
		return nil, errno
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
		cookies: cookies + "/" + fseventCookieName,
		marks:   map[string]*fseventStream{},
	}
	source.arrived.L = &source.mu
	return source, 0
}

func (source *fseventSource) canonical(path string, follow bool) (string, Errno) {
	return fseventCanonical(path, follow)
}

// fseventCanonical takes its answer from the file's open handle, so a directory
// is named the way FSEvents will name it. The stat is what gives an error its
// errno.
func fseventCanonical(path string, follow bool) (string, Errno) {
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
	path, err := BytePtrFromString(dir.path)
	if err != nil {
		return err.(Errno)
	}
	cookie := source.cookies + strconv.Itoa(Getpid()) + "-" + strconv.Itoa(int(fseventCookies.Add(1)))
	cookieName, err := BytePtrFromString(cookie)
	if err != nil {
		return err.(Errno)
	}
	stream := cosmoFSEventOpen(path, cookieName, int32(source.writeFd), source.queue)
	if stream == 0 {
		return EIO
	}
	if errno := fseventTouch(cookie); errno != 0 {
		cosmoFSEventClose(stream)
		return errno
	}
	owner := &fseventStream{stream: stream, cookie: cookie, since: ^uint64(0)}
	dir.host = owner
	source.marks[cookie] = owner
	return 0
}

// fseventTouch gives a stream its cookie event.
func fseventTouch(cookie string) Errno {
	fd, err := Open(cookie, O_CREAT|O_EXCL|O_WRONLY|O_CLOEXEC, 0o600)
	if err != nil {
		return err.(Errno)
	}
	Close(fd)
	if err := Unlink(cookie); err != nil {
		return err.(Errno)
	}
	return 0
}

func (source *fseventSource) close(dir *inotifyDir) {
	owner := dir.host.(*fseventStream)
	if owner.stream == 0 {
		return
	}
	if source.marks[owner.cookie] == owner {
		owner.closed = true
		return
	}
	cosmoFSEventClose(owner.stream)
}

// settle says a stream's cookie has arrived, with the events waiting for it.
func (source *fseventSource) settle(owner *fseventStream, id uint64) {
	owner.since = id
	delete(source.marks, owner.cookie)
	if owner.closed {
		cosmoFSEventClose(owner.stream)
	}
	if len(source.marks) > 0 {
		return
	}
	held := source.held
	source.held = nil
	for _, item := range held {
		source.event(item.id, item.flags, item.path)
	}
}

// moved follows a directory to its new path. A stream watches a path, so what
// happened since the rename is only in the directory itself.
func (source *fseventSource) moved(dir *inotifyDir, old string) {
	source.close(dir)
	if source.open(dir) != 0 {
		dir.host = &fseventStream{}
		source.ino.lost(dir)
		return
	}
	source.ino.resync(dir)
}

// stop runs after every directory has closed. The streams still waiting for a
// cookie stop here. The pipe closes when the last callback has finished, which
// ends pump and then run.
func (source *fseventSource) stop() {
	source.ino.mu.Lock()
	for _, owner := range source.marks {
		cosmoFSEventClose(owner.stream)
	}
	clear(source.marks)
	source.ino.mu.Unlock()
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
		source.record(id, flags, string(buf[fseventHeaderBytes:end]))
		buf = buf[end:]
	}
	return append([]byte(nil), buf...)
}

func (source *fseventSource) record(id uint64, flags uint32, path string) {
	if owner := source.marks[path]; owner != nil {
		source.settle(owner, id)
		return
	}
	cookies := len(source.cookies)
	if len(path) > cookies && path[:cookies] == source.cookies || !source.first(id) {
		return
	}
	if flags&fseventDropped != 0 {
		for _, owner := range source.marks {
			source.settle(owner, 0)
		}
		for _, dir := range source.ino.paths {
			source.ino.resync(dir)
		}
		return
	}
	if len(source.marks) > 0 {
		source.held = append(source.held, fseventRecord{id, flags, path})
		return
	}
	source.event(id, flags, path)
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

// reports says whether a directory's watch began before an event.
func (source *fseventSource) reports(dir *inotifyDir, id uint64) bool {
	return id > dir.host.(*fseventStream).since
}

func (source *fseventSource) event(id uint64, flags uint32, path string) {
	ino := source.ino
	if dir := ino.paths[path]; dir != nil && source.reports(dir, id) {
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
	if dir := ino.paths[above]; dir != nil && !dir.closing && source.reports(dir, id) {
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
