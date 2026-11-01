// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// inotify on an NT host, built on ReadDirectoryChangesW. The descriptor a
// caller holds is one end of a socket pair, because a socket is what the NT
// poller and nonblocking reads work on. One goroutine takes every directory's
// changes from a completion port and writes records to the other end.

package syscall

import (
	"internal/bytealg"
	"internal/runtime/syscall/cosmo"
	"sync"
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

	// ntInFlightMax bounds the bytes written and not yet read.
	ntInFlightMax = 4096

	// ntHoldMillis is how long a removed name waits for the rest of a rename.
	ntHoldMillis = 30

	// ntRetryMillis is how soon records waiting for the reader are tried again.
	ntRetryMillis = 5
)

// An ntNotifyInformation is the head of Win32's FILE_NOTIFY_INFORMATION, which
// the name follows.
type ntNotifyInformation struct {
	next      uint32
	action    uint32
	nameBytes uint32
}

// An ntEntry is what a stat says about a watched file or a child of a watched
// directory.
type ntEntry struct {
	id    epollID
	isDir bool
	mode  uint32
	size  int64
	mtime Timespec

	dir    *ntDirectory // holding it as a child
	name   string
	waited uint32 // milliseconds held since its removal
}

func ntStat(path string) *ntEntry {
	var stat Stat_t
	if err := Lstat(path, &stat); err != nil {
		return nil
	}
	return &ntEntry{
		id:    epollID{stat.Dev, stat.Ino},
		isDir: stat.Mode&S_IFMT == S_IFDIR,
		mode:  stat.Mode,
		size:  stat.Size,
		mtime: stat.Mtim,
	}
}

func (entry *ntEntry) dirBit() uint32 {
	if entry.isDir {
		return IN_ISDIR
	}
	return 0
}

// An ntDirectory is one open directory and its read in flight. It serves the
// watch on the directory itself and the watches on names in it. NT reports the
// it, whose own events. NT reports only to their parent.
type ntDirectory struct {
	key    uintptr
	path   string
	handle uintptr

	buffer     []byte
	overlapped [4]uintptr
	armed      bool // a read is in flight
	closing    bool

	watch    *ntWatch            // nil unless the directory is watched
	children map[string]*ntEntry // nil unless the directory is watched
	selves   map[string]*ntWatch // the watches on names here
	renaming string              // the old name, until its new one arrives
}

func (dir *ntDirectory) join(name string) string {
	return dir.path + "/" + name
}

// list reads the names in the directory. ok is false when that says nothing
// about them.
func (dir *ntDirectory) list() (entries []inotifyEntry, ok bool) {
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

type ntWatch struct {
	wd    int32
	mask  uint32
	entry ntEntry
	gone  bool

	dir    *ntDirectory // nil unless a directory
	parent *ntDirectory // nil at a drive root
	name   string       // in the parent
}

type ntInotify struct {
	fd      int // the end callers hold
	writeFd int
	port    uintptr
	done    chan struct{}

	mu          sync.Mutex
	closed      bool
	watches     map[int32]*ntWatch
	files       map[epollID]*ntWatch
	directories map[uintptr]*ntDirectory
	paths       map[string]*ntDirectory
	ids         map[epollID]*ntEntry // the children of every watched directory
	held        []*ntEntry           // removed, and maybe half of a rename
	lastWd      int32
	lastKey     uintptr
	lastCookie  uint32
	queue       inotifyQueue
	batch       []byte
}

var ntInotifies struct {
	sync.Mutex
	m map[int]*ntInotify
}

func ntInotifyInit(flags uintptr) (uintptr, Errno) {
	var pair [2]int32
	if err := socketpair(AF_UNIX, SOCK_STREAM|SOCK_CLOEXEC, 0, &pair); err != nil {
		return 0, err.(Errno)
	}
	reader, writer := int(pair[0]), int(pair[1])
	port, errno := ntInotifySetup(reader, flags)
	if errno != 0 {
		Close(reader)
		Close(writer)
		return 0, errno
	}
	ino := &ntInotify{
		fd:          reader,
		writeFd:     writer,
		port:        port,
		done:        make(chan struct{}),
		watches:     map[int32]*ntWatch{},
		files:       map[epollID]*ntWatch{},
		directories: map[uintptr]*ntDirectory{},
		paths:       map[string]*ntDirectory{},
		ids:         map[epollID]*ntEntry{},
	}

	ntInotifies.Lock()
	if ntInotifies.m == nil {
		ntInotifies.m = map[int]*ntInotify{}
	}
	ntInotifies.m[reader] = ino
	ntInotifies.Unlock()
	go ino.run()
	go ino.monitor()
	return uintptr(reader), 0
}

// ntInotifySetup gives the caller's end the flags inotify_init1 asked for and
// makes the completion port.
func ntInotifySetup(fd int, flags uintptr) (uintptr, Errno) {
	if flags&IN_NONBLOCK != 0 {
		if err := SetNonblock(fd, true); err != nil {
			return 0, err.(Errno)
		}
	}
	if flags&IN_CLOEXEC == 0 {
		if _, err := fcntl(fd, F_SETFD, 0); err != nil {
			return 0, err.(Errno)
		}
	}
	port, failure := cosmo.Windows().WatchPort()
	return port, Errno(failure)
}

// ntInotifyLookup returns the instance fd refers to.
func ntInotifyLookup(fd int) (*ntInotify, Errno) {
	var stat Stat_t
	if err := Fstat(fd, &stat); err != nil {
		return nil, err.(Errno)
	}
	ntInotifies.Lock()
	defer ntInotifies.Unlock()
	ino := ntInotifies.m[fd]
	if ino == nil {
		return nil, EINVAL
	}
	return ino, 0
}

func ntInotifyAddWatch(fd int, path *byte, mask uint32) (uintptr, Errno) {
	ino, errno := ntInotifyLookup(fd)
	if errno != 0 {
		return 0, errno
	}
	if path == nil {
		return 0, EFAULT
	}
	if !inotifyMaskValid(mask) {
		return 0, EINVAL
	}
	full, failure := cosmo.Windows().FinalPath(inotifyPath(path), mask&IN_DONT_FOLLOW == 0)
	if failure != 0 {
		return 0, Errno(failure)
	}
	entry := ntStat(full)
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
	watch := &ntWatch{wd: ino.lastWd, mask: mask & inotifyKept, entry: *entry}
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

func ntInotifyRmWatch(fd int, wd int32) Errno {
	ino, errno := ntInotifyLookup(fd)
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
	cosmo.Windows().WatchWake(ino.port)
	return 0
}

// directory returns the open directory at path, opening it if need be.
func (ino *ntInotify) directory(path string) (*ntDirectory, Errno) {
	if dir := ino.paths[path]; dir != nil {
		return dir, 0
	}
	ino.lastKey++
	handle, failure := cosmo.Windows().WatchOpen(path, ino.port, ino.lastKey)
	if failure != 0 {
		return nil, Errno(failure)
	}
	dir := &ntDirectory{
		key:    ino.lastKey,
		path:   path,
		handle: handle,
		buffer: make([]byte, ntWatchBuffer),
		selves: map[string]*ntWatch{},
	}
	ino.directories[dir.key] = dir
	ino.paths[path] = dir
	if ino.read(dir) != 0 {
		ino.retire(dir)
		return nil, EIO
	}
	return dir, 0
}

// watchDirectory starts reading before it lists the children, so that nothing
// happens between both unseen.
func (ino *ntInotify) watchDirectory(watch *ntWatch, path string) Errno {
	dir, errno := ino.directory(path)
	if errno != 0 {
		return errno
	}
	watch.dir = dir
	dir.watch = watch
	dir.children = map[string]*ntEntry{}
	entries, ok := dir.list()
	if !ok {
		return EIO
	}
	for _, item := range entries {
		if child := ntStat(dir.join(item.name)); child != nil {
			ino.setChild(dir, item.name, child)
		}
	}
	return 0
}

// attach makes the directory holding a watched path report it. A directory
// watch works without this, losing only IN_MOVE_SELF. Nothing else tells a
// file watch anything.
func (ino *ntInotify) attach(watch *ntWatch, path string) Errno {
	slash := bytealg.LastIndexByteString(path, '/')
	if slash <= 0 {
		return 0
	}
	parent, errno := ino.directory(path[:slash])
	if errno != 0 {
		return errno
	}
	watch.parent = parent
	watch.name = path[slash+1:]
	parent.selves[watch.name] = watch
	return 0
}

func (ino *ntInotify) setChild(dir *ntDirectory, name string, entry *ntEntry) {
	entry.dir, entry.name = dir, name
	dir.children[name] = entry
	ino.ids[entry.id] = entry
}

func (ino *ntInotify) dropChild(entry *ntEntry) {
	if entry.dir.children[entry.name] == entry {
		delete(entry.dir.children, entry.name)
	}
	if ino.ids[entry.id] == entry {
		delete(ino.ids, entry.id)
	}
}

// read starts the directory's next read and reports a Win32 error.
func (ino *ntInotify) read(dir *ntDirectory) uintptr {
	dir.overlapped = [4]uintptr{}
	failure := cosmo.Windows().WatchRead(dir.handle, dir.buffer, &dir.overlapped)
	dir.armed = failure == 0
	return failure
}

// retire stops using a directory. The kernel owns its buffer until the
// cancelled read's completion arrives, so that is when it closes.
func (ino *ntInotify) retire(dir *ntDirectory) {
	dir.closing = true
	if ino.paths[dir.path] == dir {
		delete(ino.paths, dir.path)
	}
	if dir.armed {
		cosmo.Windows().WatchCancel(dir.handle)
		return
	}
	ino.closeDirectory(dir)
}

func (ino *ntInotify) closeDirectory(dir *ntDirectory) {
	cosmo.Windows().WatchClose(dir.handle)
	delete(ino.directories, dir.key)
}

func (ino *ntInotify) retireIdle(dir *ntDirectory) {
	if !dir.closing && dir.watch == nil && len(dir.selves) == 0 {
		ino.retire(dir)
	}
}

func (ino *ntInotify) dropWatch(watch *ntWatch) {
	watch.gone = true
	delete(ino.watches, watch.wd)
	delete(ino.files, watch.entry.id)
	if parent := watch.parent; parent != nil {
		delete(parent.selves, watch.name)
		watch.parent = nil
		ino.retireIdle(parent)
	}
	if dir := watch.dir; dir != nil {
		for _, child := range dir.children {
			ino.dropChild(child)
		}
		dir.children = nil
		dir.watch = nil
		watch.dir = nil
		ino.retireIdle(dir)
	}
}

// emit queues an event the watch's mask asks for. A oneshot watch ends here.
func (ino *ntInotify) emit(watch *ntWatch, mask, cookie uint32, name string) {
	if watch == nil || watch.gone || watch.mask&mask&IN_ALL_EVENTS == 0 {
		return
	}
	ino.queue.push(watch.wd, mask, cookie, name)
	if watch.mask&IN_ONESHOT != 0 {
		ino.dropWatch(watch)
		ino.queue.push(watch.wd, IN_IGNORED, 0, "")
	}
}

// modified reports a write. NT has no event for a close, so a write also stands
// in for the IN_CLOSE_WRITE that ends it.
func (ino *ntInotify) modified(watch *ntWatch, name string) {
	ino.emit(watch, IN_MODIFY, 0, name)
	ino.emit(watch, IN_CLOSE_WRITE, 0, name)
}

// changed compares a stat with the last one. A directory's write time moves
// with its children, which is not an event on it.
func (ino *ntInotify) changed(watch *ntWatch, name string, entry, fresh *ntEntry) {
	wrote := !entry.isDir && (entry.size != fresh.size || entry.mtime != fresh.mtime)
	attrib := entry.mode != fresh.mode
	bit := entry.dirBit()
	entry.size, entry.mtime, entry.mode = fresh.size, fresh.mtime, fresh.mode
	if attrib {
		ino.emit(watch, IN_ATTRIB|bit, 0, name)
	}
	if wrote {
		ino.modified(watch, name)
	}
}

// heldNamed returns the held removal of a name.
func (ino *ntInotify) heldNamed(dir *ntDirectory, name string) *ntEntry {
	for _, entry := range ino.held {
		if entry.dir == dir && entry.name == name {
			return entry
		}
	}
	return nil
}

// unhold takes a removal back, and takeHeld finds the one of a file.
func (ino *ntInotify) unhold(entry *ntEntry) {
	for idx, held := range ino.held {
		if held == entry {
			ino.held = append(ino.held[:idx], ino.held[idx+1:]...)
			return
		}
	}
}

func (ino *ntInotify) takeHeld(id epollID) *ntEntry {
	for _, entry := range ino.held {
		if entry.id == id {
			ino.unhold(entry)
			return entry
		}
	}
	return nil
}

// age reports the removals that have waited long enough to be removals.
func (ino *ntInotify) age(millis uint32) {
	kept := ino.held[:0]
	for _, entry := range ino.held {
		entry.waited += millis
		if entry.waited < ntHoldMillis {
			kept = append(kept, entry)
			continue
		}
		ino.emit(entry.dir.watch, IN_DELETE|entry.dirBit(), 0, entry.name)
	}
	ino.held = kept
}

// movedFrom finds a child that has left for dir before the removal arrived.
func (ino *ntInotify) movedFrom(id epollID, dir *ntDirectory, name string) *ntEntry {
	entry := ino.ids[id]
	if entry == nil || entry.dir == dir && entry.name == name {
		return nil
	}
	if still := ntStat(entry.dir.join(entry.name)); still != nil && still.id == id {
		return nil
	}
	return entry
}

// pair reports one rename. from can belong to another directory.
func (ino *ntInotify) pair(from *ntEntry, dir *ntDirectory, name string, to *ntEntry) {
	ino.lastCookie++
	cookie := ino.lastCookie
	bit := to.dirBit()
	ino.emit(from.dir.watch, IN_MOVED_FROM|bit, cookie, from.name)
	ino.dropChild(from)
	if dir.children != nil {
		ino.setChild(dir, name, to)
	}
	ino.emit(dir.watch, IN_MOVED_TO|bit, cookie, name)
}

func (ino *ntInotify) added(dir *ntDirectory, name string) {
	if dir.children == nil {
		return
	}
	if existing := dir.children[name]; existing != nil {
		ino.dropChild(existing)
	}
	fresh := ntStat(dir.join(name))
	if fresh == nil {
		ino.emit(dir.watch, IN_CREATE, 0, name)
		return
	}
	from := ino.takeHeld(fresh.id)
	if from == nil {
		from = ino.movedFrom(fresh.id, dir, name)
	}
	if from != nil {
		ino.pair(from, dir, name, fresh)
		return
	}
	if ino.heldNamed(dir, name) != nil {
		ino.age(ntHoldMillis)
	}
	ino.setChild(dir, name, fresh)
	ino.emit(dir.watch, IN_CREATE|fresh.dirBit(), 0, name)
	if fresh.size > 0 {
		ino.modified(dir.watch, name)
	}
}

func (ino *ntInotify) removed(dir *ntDirectory, name string) {
	if watch := dir.selves[name]; watch != nil {
		ino.selfRemoved(watch)
	}
	if child := dir.children[name]; child != nil {
		ino.dropChild(child)
		ino.held = append(ino.held, child)
	}
}

func (ino *ntInotify) modifiedName(dir *ntDirectory, name string) {
	child, watch := dir.children[name], dir.selves[name]
	if child == nil && watch == nil && dir.children == nil {
		return
	}
	fresh := ntStat(dir.join(name))
	if fresh == nil {
		return
	}
	if child != nil {
		ino.changed(dir.watch, name, child, fresh)
	} else if dir.children != nil {
		ino.setChild(dir, name, fresh)
	}
	if watch != nil {
		ino.changed(watch, "", &watch.entry, fresh)
	}
}

func (ino *ntInotify) renamedOld(dir *ntDirectory, name string) {
	dir.renaming = name
	if watch := dir.selves[name]; watch != nil {
		ino.emit(watch, IN_MOVE_SELF|watch.entry.dirBit(), 0, "")
	}
}

func (ino *ntInotify) renamedNew(dir *ntDirectory, name string) {
	old := dir.renaming
	dir.renaming = ""
	if replaced := ino.heldNamed(dir, name); replaced != nil {
		ino.unhold(replaced)
	}
	if old == "" {
		ino.added(dir, name)
		return
	}
	if dir.children != nil {
		ino.renameChild(dir, old, name)
	}
	if replaced := dir.selves[name]; replaced != nil {
		ino.selfGone(replaced)
	}
	watch := dir.selves[old]
	if watch == nil {
		return
	}
	delete(dir.selves, old)
	dir.selves[name], watch.name = watch, name
	if watch.dir != nil {
		ino.rehome(watch.dir, dir.join(name))
	}
}

func (ino *ntInotify) renameChild(dir *ntDirectory, old, name string) {
	from := dir.children[old]
	if from == nil {
		from = &ntEntry{dir: dir, name: old}
	}
	if existing := dir.children[name]; existing != nil {
		ino.dropChild(existing)
	}
	fresh := ntStat(dir.join(name))
	if fresh == nil {
		copied := *from
		fresh = &copied
	}
	ino.pair(from, dir, name, fresh)
}

// rehome records a watched directory's new path, and those of the watched
// directories under it.
func (ino *ntInotify) rehome(dir *ntDirectory, path string) {
	prefix := dir.path + "/"
	if ino.paths[dir.path] == dir {
		delete(ino.paths, dir.path)
	}
	for _, other := range ino.directories {
		if len(other.path) > len(prefix) && other.path[:len(prefix)] == prefix {
			delete(ino.paths, other.path)
			other.path = path + "/" + other.path[len(prefix):]
			ino.paths[other.path] = other
		}
	}
	dir.path = path
	ino.paths[path] = dir
}

// selfRemoved handles a watched name leaving its parent. An open directory
// that has another path now moved there. NT says nothing else that tells a
// watched file's move out of the directory from its removal.
func (ino *ntInotify) selfRemoved(watch *ntWatch) {
	if dir := watch.dir; dir != nil {
		if path, failure := cosmo.Windows().WatchPath(dir.handle); failure == 0 && path != dir.path {
			ino.emit(watch, IN_MOVE_SELF|IN_ISDIR, 0, "")
			if watch.gone {
				return
			}
			parent := watch.parent
			delete(parent.selves, watch.name)
			watch.parent = nil
			ino.retireIdle(parent)
			ino.rehome(dir, path)
			ino.attach(watch, path)
			return
		}
	}
	ino.selfGone(watch)
}

func (ino *ntInotify) selfGone(watch *ntWatch) {
	ino.age(ntHoldMillis)
	ino.emit(watch, IN_DELETE_SELF|watch.entry.dirBit(), 0, "")
	if !watch.gone {
		ino.dropWatch(watch)
		ino.queue.push(watch.wd, IN_IGNORED, 0, "")
	}
}

// lost handles a directory whose read failed, which is how a removed directory
// reports its removal.
func (ino *ntInotify) lost(dir *ntDirectory) {
	if dir.watch != nil {
		ino.selfGone(dir.watch)
	}
	for _, watch := range dir.selves {
		ino.selfGone(watch)
	}
}

// resync recovers from a change list too long for the buffer, which NT drops
// whole, by comparing the directory with what is recorded of it.
func (ino *ntInotify) resync(dir *ntDirectory) {
	if entries, ok := dir.list(); ok && dir.children != nil {
		present := make(map[string]bool, len(entries))
		for _, item := range entries {
			present[item.name] = true
			if dir.children[item.name] == nil {
				ino.added(dir, item.name)
			} else {
				ino.modifiedName(dir, item.name)
			}
		}
		for name := range dir.children {
			if !present[name] {
				ino.removed(dir, name)
			}
		}
	}
	for name := range dir.selves {
		if ntStat(dir.join(name)) == nil {
			ino.removed(dir, name)
		} else {
			ino.modifiedName(dir, name)
		}
	}
}

func (ino *ntInotify) record(dir *ntDirectory, action uint32, name string) {
	switch action {
	case ntActionAdded:
		ino.added(dir, name)
	case ntActionRemoved:
		ino.removed(dir, name)
	case ntActionModified:
		ino.modifiedName(dir, name)
	case ntActionRenamedOld:
		ino.renamedOld(dir, name)
	case ntActionRenamedNew:
		ino.renamedNew(dir, name)
	}
}

// records applies a FILE_NOTIFY_INFORMATION list.
func (ino *ntInotify) records(dir *ntDirectory, buf []byte) {
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
		ino.record(dir, info.action, name)
		if info.next == 0 || uint64(info.next) > uint64(len(buf)) {
			return
		}
		buf = buf[info.next:]
	}
}

// complete takes a finished read and starts the next.
func (ino *ntInotify) complete(key uintptr, transferred uint32, failure uintptr) {
	dir := ino.directories[key]
	if dir == nil {
		return
	}
	dir.armed = false
	if dir.closing {
		ino.closeDirectory(dir)
		return
	}
	switch {
	case failure == ntErrorOperationAborted:
	case failure != 0 && failure != ntErrorNotifyEnumDir:
		ino.lost(dir)
		return
	case transferred == 0:
		ino.resync(dir)
	default:
		ino.records(dir, dir.buffer[:transferred])
	}
	if !dir.closing && ino.read(dir) != 0 {
		ino.lost(dir)
	}
}

// flush writes as many whole records as ntInFlightMax leaves room for.
func (ino *ntInotify) flush() {
	if ino.closed {
		return
	}
	unread, failure := cosmo.Windows().SocketUnread(int32(ino.fd))
	if failure != 0 {
		return
	}
	batch := ino.batch[:0]
	for len(ino.queue.records) > 0 {
		record := ino.queue.records[0]
		if unread+len(batch)+len(record) > ntInFlightMax {
			break
		}
		batch = append(batch, record...)
		ino.queue.records[0] = nil
		ino.queue.records = ino.queue.records[1:]
	}
	ino.batch = batch
	if len(ino.queue.records) == 0 {
		ino.queue.records = nil
	}
	if len(batch) > 0 {
		write(ino.writeFd, batch)
	}
}

// waitMillis says how long the port may stay quiet.
func (ino *ntInotify) waitMillis() uint32 {
	switch {
	case len(ino.queue.records) > 0:
		return ntRetryMillis
	case len(ino.held) > 0:
		return ntHoldMillis
	}
	return ntWaitForever
}

// run turns completions into records until the caller's end is closed and the
// last read has come back.
func (ino *ntInotify) run() {
	fns := cosmo.Windows()
	for {
		ino.mu.Lock()
		wait := ino.waitMillis()
		ino.mu.Unlock()
		transferred, key, completed, failure := fns.WatchWait(ino.port, wait)
		ino.mu.Lock()
		if completed {
			ino.complete(key, transferred, failure)
			ino.age(1)
		} else if wait != ntWaitForever {
			ino.age(wait)
		}
		ino.flush()
		finished := ino.closed && len(ino.directories) == 0
		ino.mu.Unlock()
		if finished {
			break
		}
	}
	Close(ino.writeFd)
	fns.WatchClose(ino.port)
	close(ino.done)
}

// monitor blocks in a read of the other end, which returns once the caller's
// end is closed. A nonblocking caller's closing it is seen nowhere else.
func (ino *ntInotify) monitor() {
	buf := make([]byte, 16)
	for {
		count, err := read(ino.writeFd, buf)
		if err != nil || count <= 0 {
			break
		}
	}
	ntInotifies.Lock()
	if ntInotifies.m[ino.fd] == ino {
		delete(ntInotifies.m, ino.fd)
	}
	ntInotifies.Unlock()

	ino.mu.Lock()
	ino.closed = true
	for _, watch := range ino.watches {
		ino.dropWatch(watch)
	}
	ino.held = nil
	ino.queue.records = nil
	ino.mu.Unlock()
	cosmo.Windows().WatchWake(ino.port)
}
