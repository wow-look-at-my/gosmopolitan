// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// The inotify calls on every host. A host's file watching only says which names
// in a directory may have changed. This file and inotify_events_cosmo.go decide
// what happened from that hint and a stat, so one program sees one behavior.
//
// The descriptor a caller holds is one end of a socket pair, because a socket is
// what the poller on every host works on. Records go to the other end.

package syscall

import (
	"internal/bytealg"
	"internal/runtime/syscall/cosmo"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	// inotifyInFlightMax bounds the bytes written and not yet read.
	inotifyInFlightMax = 4096

	// inotifyHoldMillis is how long a name that left waits for the other half of a rename.
	inotifyHoldMillis = 30

	// inotifyTickMillis is how often held names and unwritten records are looked at while there are any.
	inotifyTickMillis = 5
)

// An inotifySource is one host's way of hearing about changes.
type inotifySource interface {
	// canonical returns the path the host reports a file under.
	canonical(path string, follow bool) (string, Errno)
	// open starts hearing about the directory's names.
	open(dir *inotifyDir) Errno
	// close stops hearing about them.
	close(dir *inotifyDir)
	// moved says the directory now lives at dir.path.
	moved(dir *inotifyDir, old string)
	// run delivers hints until stop ends it.
	run(ino *inotifyInstance)
	// stop ends run, after every directory has closed.
	stop()
}

// An inotifyFile is what a stat says about a watched name.
type inotifyFile struct {
	id      epollID
	isDir   bool
	regular bool
	mode    uint32
	uid     uint32
	gid     uint32
	size    int64
	mtime   Timespec

	dir    *inotifyDir // the directory holding the name
	name   string
	waited uint32 // milliseconds held since it left
}

func inotifyStat(path string) *inotifyFile {
	var stat Stat_t
	if err := Lstat(path, &stat); err != nil {
		return nil
	}
	return &inotifyFile{
		id:      epollID{stat.Dev, stat.Ino},
		isDir:   stat.Mode&S_IFMT == S_IFDIR,
		regular: stat.Mode&S_IFMT == S_IFREG,
		mode:    stat.Mode,
		uid:     stat.Uid,
		gid:     stat.Gid,
		size:    stat.Size,
		mtime:   stat.Mtim,
	}
}

func (entry *inotifyFile) dirBit() uint32 {
	if entry.isDir {
		return IN_ISDIR
	}
	return 0
}

// An inotifyDir is one directory the source hears about. It serves the watch
// on the directory and the watches on names in it.
type inotifyDir struct {
	path    string
	host    any // the source's own state
	closing bool

	watch    *inotifyWatch            // nil unless the directory is watched
	children map[string]*inotifyFile  // nil unless the directory is watched
	selves   map[string]*inotifyWatch // the watches on names here
}

func (dir *inotifyDir) join(name string) string {
	if dir.path == "/" {
		return "/" + name
	}
	return dir.path + "/" + name
}

// list reads the names in the directory. ok is false when that says nothing
// about them.
func (dir *inotifyDir) list() (entries []inotifyEntry, ok bool) {
	fd, err := Open(dir.path, O_RDONLY|O_DIRECTORY|O_CLOEXEC, 0)
	if err != nil {
		return nil, false
	}
	defer Close(fd)
	buf := make([]byte, 16384)
	for {
		count, err := Getdents(fd, buf)
		if err != nil {
			return nil, false
		}
		if count <= 0 {
			return entries, true
		}
		entries = inotifyParse(buf[:count], entries)
	}
}

type inotifyWatch struct {
	wd    int32
	mask  uint32
	entry inotifyFile
	gone  bool

	dir    *inotifyDir // nil unless a directory
	parent *inotifyDir // nil at the root
	name   string      // in the parent
}

type inotifyInstance struct {
	fd       int
	writeFd  int
	source   inotifySource
	done     chan struct{} // closed when every descriptor has gone
	stopping chan struct{} // closed when the caller's end has gone
	reading  sync.Mutex    // held while a read looks at the records it takes

	mu         sync.Mutex
	busy       sync.Cond // something waits for the clock
	closed     bool
	watches    map[int32]*inotifyWatch
	files      map[epollID]*inotifyWatch
	paths      map[string]*inotifyDir
	ids        map[epollID]*inotifyFile // the children of every watched directory
	held       []*inotifyFile           // left, and maybe half of a rename
	lastWd     int32
	lastCookie uint32
	queue      inotifyQueue
	batch      []byte
}

// inotifies finds an instance from its descriptor.
var (
	inotifies struct {
		sync.Mutex
		m map[int]*inotifyInstance
	}
	inotifyFds  unsafe.Pointer
	inotifyLive atomic.Int32
)

// inotifyPublish makes inotifyFds match the table. The caller holds its lock.
func inotifyPublish() {
	if len(inotifies.m) == 0 {
		atomic.StorePointer(&inotifyFds, nil)
		return
	}
	list := make([]int32, 0, len(inotifies.m))
	for fd := range inotifies.m {
		list = append(list, int32(fd))
	}
	atomic.StorePointer(&inotifyFds, unsafe.Pointer(&list))
}

// inotifySleep is time.Sleep, which this package cannot import.
func inotifySleep(nanos int64)

func inotifyNewSource(ino *inotifyInstance) (inotifySource, Errno) {
	switch {
	case cosmo.Windows() != nil:
		return ntNewSource(ino)
	case cosmo.Darwin():
		return fseventNewSource(ino)
	}
	return kernelNewSource(ino)
}

func inotifyInit(flags uintptr) (uintptr, Errno) {
	if flags&^(IN_CLOEXEC|IN_NONBLOCK) != 0 {
		return 0, EINVAL
	}
	var pair [2]int32
	if errno := inotifyPair(&pair); errno != 0 {
		return 0, errno
	}
	reader, writer := int(pair[0]), int(pair[1])
	ino := &inotifyInstance{
		fd:       reader,
		writeFd:  writer,
		done:     make(chan struct{}),
		stopping: make(chan struct{}),
		watches:  map[int32]*inotifyWatch{},
		files:    map[epollID]*inotifyWatch{},
		paths:    map[string]*inotifyDir{},
		ids:      map[epollID]*inotifyFile{},
	}
	ino.busy.L = &ino.mu
	errno := inotifySetup(reader, flags)
	if errno == 0 {
		ino.source, errno = inotifyNewSource(ino)
	}
	if errno != 0 {
		Close(reader)
		Close(writer)
		return 0, errno
	}

	inotifies.Lock()
	if inotifies.m == nil {
		inotifies.m = map[int]*inotifyInstance{}
	}
	inotifies.m[reader] = ino
	inotifyPublish()
	inotifies.Unlock()
	inotifyLive.Add(1)
	go ino.run()
	go ino.clock()
	go ino.monitor()
	return uintptr(reader), 0
}

func inotifyPair(pair *[2]int32) Errno {
	if windows := cosmo.Windows(); windows != nil {
		return Errno(windows.SocketPair(pair))
	}
	if err := socketpair(AF_UNIX, SOCK_STREAM|SOCK_CLOEXEC, 0, pair); err != nil {
		return err.(Errno)
	}
	return 0
}

// inotifySetup gives the caller's end the flags inotify_init1 asked for.
func inotifySetup(fd int, flags uintptr) Errno {
	if flags&IN_NONBLOCK != 0 {
		if err := SetNonblock(fd, true); err != nil {
			return err.(Errno)
		}
	}
	if flags&IN_CLOEXEC == 0 {
		if _, err := fcntl(fd, F_SETFD, 0); err != nil {
			return err.(Errno)
		}
	}
	return 0
}

// inotifyLookup returns the instance fd refers to.
func inotifyLookup(fd int) (*inotifyInstance, Errno) {
	var stat Stat_t
	if err := Fstat(fd, &stat); err != nil {
		return nil, err.(Errno)
	}
	inotifies.Lock()
	defer inotifies.Unlock()
	ino := inotifies.m[fd]
	if ino == nil {
		return nil, EINVAL
	}
	return ino, 0
}

func inotifyAddWatch(fd int, path *byte, mask uint32) (uintptr, Errno) {
	ino, errno := inotifyLookup(fd)
	if errno != 0 {
		return 0, errno
	}
	if path == nil {
		return 0, EFAULT
	}
	if !inotifyMaskValid(mask) {
		return 0, EINVAL
	}
	full, errno := ino.source.canonical(inotifyPath(path), mask&IN_DONT_FOLLOW == 0)
	if errno != 0 {
		return 0, errno
	}
	entry := inotifyStat(full)
	if entry == nil {
		return 0, ENOENT
	}
	if mask&IN_ONLYDIR != 0 && !entry.isDir {
		return 0, ENOTDIR
	}

	ino.mu.Lock()
	defer ino.mu.Unlock()
	if ino.closed {
		return 0, EBADF
	}
	if watch := ino.files[entry.id]; watch != nil {
		if mask&inMaskCreate != 0 {
			return 0, EEXIST
		}
		watch.mask = inotifyMerged(watch.mask, mask)
		return uintptr(watch.wd), 0
	}

	ino.lastWd++
	watch := &inotifyWatch{wd: ino.lastWd, mask: mask & inotifyKept, entry: *entry}
	ino.watches[watch.wd] = watch
	ino.files[entry.id] = watch
	if entry.isDir {
		errno = ino.watchDirectory(watch, full)
	}
	if selfErrno := ino.attach(watch, full); !entry.isDir {
		errno = selfErrno
	}
	if errno != 0 {
		ino.dropWatch(watch)
		return 0, errno
	}
	return uintptr(watch.wd), 0
}

func inotifyRmWatch(fd int, wd int32) Errno {
	ino, errno := inotifyLookup(fd)
	if errno != 0 {
		return errno
	}
	ino.mu.Lock()
	defer ino.mu.Unlock()
	watch := ino.watches[wd]
	if watch == nil {
		return EINVAL
	}
	ino.dropWatch(watch)
	ino.queue.push(wd, IN_IGNORED, 0, "")
	ino.flush()
	return 0
}

// directory returns the directory at path, opening it if need be.
func (ino *inotifyInstance) directory(path string) (*inotifyDir, Errno) {
	if dir := ino.paths[path]; dir != nil {
		return dir, 0
	}
	dir := &inotifyDir{path: path, selves: map[string]*inotifyWatch{}}
	if errno := ino.source.open(dir); errno != 0 {
		return nil, errno
	}
	ino.paths[path] = dir
	return dir, 0
}

// watchDirectory starts hearing before it lists the children, so that nothing
// happens between both unseen.
func (ino *inotifyInstance) watchDirectory(watch *inotifyWatch, path string) Errno {
	dir, errno := ino.directory(path)
	if errno != 0 {
		return errno
	}
	watch.dir = dir
	dir.watch = watch
	dir.children = map[string]*inotifyFile{}
	entries, ok := dir.list()
	if !ok {
		return EIO
	}
	for _, item := range entries {
		if child := inotifyStat(dir.join(item.name)); child != nil {
			ino.setChild(dir, item.name, child)
		}
	}
	return 0
}

// attach makes the directory holding a watched path report it. A directory
// watch works without this, losing only what happens to the directory itself.
// Nothing else tells a file watch anything.
func (ino *inotifyInstance) attach(watch *inotifyWatch, path string) Errno {
	slash := bytealg.LastIndexByteString(path, '/')
	if slash < 0 || path == "/" {
		return 0
	}
	above := path[:slash]
	if slash == 0 {
		above = "/"
	}
	parent, errno := ino.directory(above)
	if errno != 0 {
		return errno
	}
	watch.parent = parent
	watch.name = path[slash+1:]
	parent.selves[watch.name] = watch
	return 0
}

func (ino *inotifyInstance) setChild(dir *inotifyDir, name string, entry *inotifyFile) {
	entry.dir, entry.name = dir, name
	dir.children[name] = entry
	if entry.id != (epollID{}) {
		ino.ids[entry.id] = entry
	}
}

func (ino *inotifyInstance) dropChild(entry *inotifyFile) {
	if entry.dir.children[entry.name] == entry {
		delete(entry.dir.children, entry.name)
	}
	if ino.ids[entry.id] == entry {
		delete(ino.ids, entry.id)
	}
}

// retire stops hearing about a directory.
func (ino *inotifyInstance) retire(dir *inotifyDir) {
	dir.closing = true
	if ino.paths[dir.path] == dir {
		delete(ino.paths, dir.path)
	}
	ino.source.close(dir)
}

// released says a directory lost a watch.
func (ino *inotifyInstance) released(dir *inotifyDir) {
	if !dir.closing && dir.watch == nil && len(dir.selves) == 0 {
		ino.retire(dir)
	}
}

func (ino *inotifyInstance) dropWatch(watch *inotifyWatch) {
	watch.gone = true
	delete(ino.watches, watch.wd)
	delete(ino.files, watch.entry.id)
	if parent := watch.parent; parent != nil {
		delete(parent.selves, watch.name)
		watch.parent = nil
		ino.released(parent)
	}
	if dir := watch.dir; dir != nil {
		for _, child := range dir.children {
			ino.dropChild(child)
		}
		dir.children = nil
		dir.watch = nil
		watch.dir = nil
		ino.released(dir)
	}
}

// rehome records a directory's new path, and those of the directories under it.
func (ino *inotifyInstance) rehome(dir *inotifyDir, path string) {
	old := dir.path
	prefix := old + "/"
	var under []*inotifyDir
	for known, other := range ino.paths {
		if len(known) > len(prefix) && known[:len(prefix)] == prefix {
			under = append(under, other)
		}
	}
	delete(ino.paths, old)
	formerly := make([]string, len(under))
	for idx, other := range under {
		formerly[idx] = other.path
		delete(ino.paths, other.path)
		other.path = path + "/" + other.path[len(prefix):]
		ino.paths[other.path] = other
	}
	dir.path = path
	ino.paths[path] = dir
	ino.source.moved(dir, old)
	for idx, other := range under {
		ino.source.moved(other, formerly[idx])
	}
}

// flush writes as many whole records as inotifyInFlightMax leaves room for.
func (ino *inotifyInstance) flush() {
	if ino.closed || ino.fd < 0 {
		return
	}
	unread, errno := inotifyUnread(ino.fd)
	if errno != 0 {
		return
	}
	batch := ino.batch[:0]
	for len(ino.queue.records) > 0 {
		record := ino.queue.records[0]
		if unread+len(batch)+len(record) > inotifyInFlightMax {
			break
		}
		batch = append(batch, record...)
		ino.queue.records[0] = nil
		ino.queue.records = ino.queue.records[1:]
	}
	ino.batch = batch
	if len(ino.queue.records) == 0 {
		ino.queue.records = nil
	} else {
		ino.busy.Signal()
	}
	if len(batch) > 0 {
		write(ino.writeFd, batch)
	}
}

// run lives as long as the source does and gives back what is left.
func (ino *inotifyInstance) run() {
	ino.source.run(ino)
	Close(ino.writeFd)
	inotifyLive.Add(-1)
	close(ino.done)
}

// clock turns held names into removals and retries records the reader had no
// room for. It sleeps on the condition while there are neither.
func (ino *inotifyInstance) clock() {
	ino.mu.Lock()
	defer ino.mu.Unlock()
	for !ino.closed {
		if len(ino.held) == 0 && len(ino.queue.records) == 0 {
			ino.busy.Wait()
			continue
		}
		ino.mu.Unlock()
		inotifySleep(inotifyTickMillis * 1000000)
		ino.mu.Lock()
		ino.age(inotifyTickMillis)
		ino.flush()
	}
}

// inotifyHangup returns once the other end of fd has closed.
func inotifyHangup(fd int) {
	buf := make([]byte, 16)
	for {
		count, err := read(fd, buf)
		if err != nil || count <= 0 {
			return
		}
	}
}

// monitor waits for every descriptor of the caller's end to close, which a
// close of one of them does not say.
func (ino *inotifyInstance) monitor() {
	inotifyHangup(ino.writeFd)

	ino.mu.Lock()
	ino.fd = -1
	ino.mu.Unlock()
	inotifies.Lock()
	for fd, same := range inotifies.m {
		if same == ino {
			delete(inotifies.m, fd)
		}
	}
	inotifyPublish()
	inotifies.Unlock()

	ino.mu.Lock()
	ino.closed = true
	for _, watch := range ino.watches {
		ino.dropWatch(watch)
	}
	ino.held = nil
	ino.queue.records = nil
	ino.busy.Broadcast()
	ino.mu.Unlock()
	close(ino.stopping)
	ino.source.stop()
}
