// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// inotify on a macOS host, built on kqueue vnode events.

package syscall

import (
	"sync"
	_ "unsafe"
)

// inotify on a macOS host, built on kqueue vnode events. The descriptor a caller holds is the read end of a pipe.

//go:linkname darwinKqueue runtime.cosmoDarwinKqueue
func darwinKqueue() (kq, errno int32)

//go:linkname darwinKevent runtime.cosmoDarwinKevent
func darwinKevent(kq int32, changes *kqueueEvent, nchanges int32, events *kqueueEvent, nevents int32, timeout *Timespec) (count, errno int32)

// A kqueueEvent is Apple's struct kevent, as runtime.keventt declares it.
type kqueueEvent struct {
	ident  uint64
	filter int16
	flags  uint16
	fflags uint32
	data   int64
	udata  *byte
}

const (
	kqFilterWrite = -2
	kqFilterVnode = -4

	kqAdd   = 0x1
	kqClear = 0x20
	kqEOF   = 0x8000

	noteDelete = 0x1
	noteWrite  = 0x2
	noteExtend = 0x4
	noteAttrib = 0x8
	noteLink   = 0x10
	noteRename = 0x20
	noteRevoke = 0x40
	noteAll    = noteDelete | noteWrite | noteExtend | noteAttrib | noteLink | noteRename | noteRevoke

	inotifyOpenFlags = O_RDONLY | O_NONBLOCK | O_CLOEXEC
	inotifyBatch     = 256
)

// An inotifyNode is a file held open and registered with the kqueue: the file
// of a watch, or a child of a watched directory.
type inotifyNode struct {
	watch *inotifyWatch
	name  string  // empty for the watch's own file
	id    epollID // kept after the close, to pair a rename's names
	fd    int
	isDir bool
	notes uint32 // gathered from the batch of events in hand

	unlinked bool // NOTE_DELETE arrived, as opposed to a rename away
	moved    bool // paired with its new name during a rescan
}

func (node *inotifyNode) dirBit() uint32 {
	if node.isDir {
		return IN_ISDIR
	}
	return 0
}

type inotifyWatch struct {
	wd       int32
	mask     uint32
	self     inotifyNode
	children map[string]*inotifyNode // nil unless a directory
	gone     bool
}

type inotifyArrival struct {
	watch *inotifyWatch
	entry inotifyEntry
}

type inotifyInstance struct {
	fd      int     // the read end, which is the descriptor callers hold
	id      epollID // of the read end
	writeFd int
	kq      int

	mu         sync.Mutex
	released   bool
	watches    map[int32]*inotifyWatch
	files      map[epollID]*inotifyWatch
	nodes      map[int]*inotifyNode
	lastWd     int32
	lastCookie uint32
	queue      inotifyQueue
}

var inotifies struct {
	sync.Mutex
	m map[int]*inotifyInstance
}

func kqueueInotifyInit(flags uintptr) (uintptr, Errno) {
	var pipe [2]int
	if err := Pipe2(pipe[:], O_CLOEXEC|O_NONBLOCK); err != nil {
		return 0, err.(Errno)
	}
	id, errno := inotifySetup(pipe[0], flags)
	var kq int
	if errno == 0 {
		kq, errno = inotifyKqueue(pipe[1])
	}
	if errno != 0 {
		Close(pipe[0])
		Close(pipe[1])
		return 0, errno
	}
	ino := &inotifyInstance{
		fd:      pipe[0],
		id:      id,
		writeFd: pipe[1],
		kq:      kq,
		watches: map[int32]*inotifyWatch{},
		files:   map[epollID]*inotifyWatch{},
		nodes:   map[int]*inotifyNode{},
	}

	inotifies.Lock()
	if inotifies.m == nil {
		inotifies.m = map[int]*inotifyInstance{}
	}
	inotifies.m[pipe[0]] = ino
	inotifies.Unlock()
	go ino.run()
	return uintptr(pipe[0]), 0
}

// inotifySetup gives the read end the flags inotify_init1 asked for.
func inotifySetup(fd int, flags uintptr) (epollID, Errno) {
	if flags&IN_CLOEXEC == 0 {
		if _, err := fcntl(fd, F_SETFD, 0); err != nil {
			return epollID{}, err.(Errno)
		}
	}
	if flags&IN_NONBLOCK == 0 {
		if err := SetNonblock(fd, false); err != nil {
			return epollID{}, err.(Errno)
		}
	}
	id, _, errno := epollIDOf(fd)
	return id, errno
}

// inotifyKqueue makes the kqueue, which fork does not pass on, and has it
// report the write end. That event carries EV_EOF once the read end is closed.
// It fires when a reader makes room for records held back in the queue.
func inotifyKqueue(writeFd int) (int, Errno) {
	kq, errno := darwinKqueue()
	if errno != 0 {
		return 0, Errno(errno)
	}
	change := kqueueEvent{ident: uint64(writeFd), filter: kqFilterWrite, flags: kqAdd | kqClear}
	if count, errno := darwinKevent(kq, &change, 1, nil, 0, nil); count < 0 {
		Close(int(kq))
		return 0, Errno(errno)
	}
	return int(kq), 0
}

// inotifyLookup returns the instance fd refers to.
func inotifyLookup(fd int) (*inotifyInstance, Errno) {
	id, _, errno := epollIDOf(fd)
	if errno != 0 {
		return nil, errno
	}
	inotifies.Lock()
	defer inotifies.Unlock()
	ino := inotifies.m[fd]
	if ino == nil || ino.id != id {
		return nil, EINVAL
	}
	return ino, 0
}

func kqueueInotifyAddWatch(fd int, path *byte, mask uint32) (uintptr, Errno) {
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
	flags := inotifyOpenFlags
	if mask&IN_ONLYDIR != 0 {
		flags |= O_DIRECTORY
	}
	if mask&IN_DONT_FOLLOW != 0 {
		flags |= O_NOFOLLOW
	}
	file, err := Open(inotifyPath(path), flags, 0)
	if err != nil {
		return 0, err.(Errno)
	}
	id, mode, errno := epollIDOf(file)
	if errno != 0 {
		Close(file)
		return 0, errno
	}

	ino.mu.Lock()
	defer ino.mu.Unlock()
	if ino.released {
		Close(file)
		return 0, EBADF
	}
	if watch := ino.files[id]; watch != nil {
		Close(file)
		if mask&inMaskCreate != 0 {
			return 0, EEXIST
		}
		watch.mask = inotifyMerged(watch.mask, mask)
		return uintptr(watch.wd), 0
	}

	ino.lastWd++
	watch := &inotifyWatch{wd: ino.lastWd, mask: mask & inotifyKept}
	watch.self = inotifyNode{watch: watch, id: id, fd: -1, isDir: mode&S_IFMT == S_IFDIR}
	if errno := ino.register(&watch.self, file); errno != 0 {
		return 0, errno
	}
	ino.watches[watch.wd] = watch
	ino.files[id] = watch
	if watch.self.isDir {
		watch.children = map[string]*inotifyNode{}
		if errno := ino.adopt(watch); errno != 0 {
			ino.dropWatch(watch)
			return 0, errno
		}
	}
	return uintptr(watch.wd), 0
}

func kqueueInotifyRmWatch(fd int, wd int32) Errno {
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

// adopt opens the children of a directory watched. Running out of descriptors
// fails the watch, as running out of watches does on Linux.
func (ino *inotifyInstance) adopt(watch *inotifyWatch) Errno {
	entries, ok := watch.readEntries()
	if !ok {
		return EIO
	}
	for _, entry := range entries {
		if _, _, errno := ino.track(watch, entry); errno != 0 {
			return errno
		}
	}
	return 0
}

// register takes ownership of fd, which it closes on failure.
func (ino *inotifyInstance) register(node *inotifyNode, fd int) Errno {
	change := kqueueEvent{ident: uint64(fd), filter: kqFilterVnode, flags: kqAdd | kqClear, fflags: noteAll}
	if count, errno := darwinKevent(int32(ino.kq), &change, 1, nil, 0, nil); count < 0 {
		Close(fd)
		return Errno(errno)
	}
	node.fd = fd
	ino.nodes[fd] = node
	return 0
}

// track records a child and returns its size. A child that cannot be opened
// stays recorded by name, so its creation and removal are still reported.
// Only running out of descriptors is an error.
func (ino *inotifyInstance) track(watch *inotifyWatch, entry inotifyEntry) (*inotifyNode, int64, Errno) {
	node := &inotifyNode{
		watch: watch,
		name:  entry.name,
		id:    epollID{watch.self.id.dev, entry.ino},
		fd:    -1,
		isDir: entry.kind == DT_DIR,
	}
	watch.children[entry.name] = node
	switch entry.kind {
	case DT_LNK, DT_FIFO, DT_SOCK, DT_CHR, DT_BLK:
		return node, 0, 0
	}
	fd, err := Openat(watch.self.fd, entry.name, inotifyOpenFlags|O_NOFOLLOW, 0)
	if err != nil {
		if err == EMFILE || err == ENFILE {
			return node, 0, err.(Errno)
		}
		return node, 0, 0
	}
	var stat Stat_t
	if err := Fstat(fd, &stat); err != nil {
		Close(fd)
		return node, 0, 0
	}
	node.isDir = stat.Mode&S_IFMT == S_IFDIR
	return node, stat.Size, ino.register(node, fd)
}

// forget closes a node, which removes it from the kqueue.
func (ino *inotifyInstance) forget(node *inotifyNode) {
	if node.fd < 0 {
		return
	}
	delete(ino.nodes, node.fd)
	Close(node.fd)
	node.fd = -1
}

func (ino *inotifyInstance) dropWatch(watch *inotifyWatch) {
	watch.gone = true
	delete(ino.watches, watch.wd)
	delete(ino.files, watch.self.id)
	ino.forget(&watch.self)
	for _, node := range watch.children {
		ino.forget(node)
	}
	watch.children = nil
}

// readEntries reads the names in a watched directory. ok is false when that
// says nothing about them. A directory already removed reads as empty.
func (watch *inotifyWatch) readEntries() (entries []inotifyEntry, ok bool) {
	if _, err := Seek(watch.self.fd, 0, 0); err != nil {
		return nil, false
	}
	buf := make([]byte, 16384)
	for {
		count, err := Getdents(watch.self.fd, buf)
		if err == ENOENT {
			return entries, true
		}
		if err != nil {
			return nil, false
		}
		if count <= 0 {
			return entries, true
		}
		entries = inotifyParse(buf[:count], entries)
	}
}

// emit queues an event the watch's mask asks for. A oneshot watch ends here.
func (ino *inotifyInstance) emit(watch *inotifyWatch, mask, cookie uint32, name string) {
	if watch.gone || watch.mask&mask&IN_ALL_EVENTS == 0 {
		return
	}
	ino.queue.push(watch.wd, mask, cookie, name)
	if watch.mask&IN_ONESHOT != 0 {
		ino.dropWatch(watch)
		ino.queue.push(watch.wd, IN_IGNORED, 0, "")
	}
}

// modified reports a write. kqueue has no event for a close, so a write also
// stands in for the IN_CLOSE_WRITE that ends it.
func (ino *inotifyInstance) modified(watch *inotifyWatch, name string) {
	ino.emit(watch, IN_MODIFY, 0, name)
	ino.emit(watch, IN_CLOSE_WRITE, 0, name)
}

// flush writes queued records until the pipe is full, which the next read
// reports through the write end's kevent. A record no longer than PIPE_BUF
// goes in whole, and the rest of a longer one is kept for the next write.
func (ino *inotifyInstance) flush() {
	for len(ino.queue.records) > 0 {
		record := ino.queue.records[0]
		count, err := write(ino.writeFd, record)
		if err != nil {
			return
		}
		if count < len(record) {
			ino.queue.records[0] = record[count:]
			continue
		}
		ino.queue.records[0] = nil
		ino.queue.records = ino.queue.records[1:]
	}
	ino.queue.records = nil
}

// inotifyWait blocks in kevent the way Syscall blocks in a system call.
//
//go:nosplit
func inotifyWait(kq int, events *kqueueEvent, capacity int32) (int, Errno) {
	runtime_entersyscall()
	count, errno := darwinKevent(int32(kq), nil, 0, events, capacity, nil)
	runtime_exitsyscall()
	return int(count), Errno(errno)
}

// run turns vnode events into records until the read end is closed, or kevent
// fails. Then the release closes the pipe, and a reader sees end of file.
func (ino *inotifyInstance) run() {
	events := make([]kqueueEvent, inotifyBatch)
	for {
		count, errno := inotifyWait(ino.kq, &events[0], inotifyBatch)
		if errno == EINTR {
			continue
		}
		closed := errno != 0
		ino.mu.Lock()
		if !closed {
			closed = ino.handle(events[:count])
		}
		ino.flush()
		ino.mu.Unlock()
		if closed {
			ino.release()
			return
		}
	}
}

func (ino *inotifyInstance) release() {
	inotifies.Lock()
	if inotifies.m[ino.fd] == ino {
		delete(inotifies.m, ino.fd)
	}
	inotifies.Unlock()

	ino.mu.Lock()
	defer ino.mu.Unlock()
	ino.released = true
	for _, watch := range ino.watches {
		ino.dropWatch(watch)
	}
	Close(ino.writeFd)
	Close(ino.kq)
}

// handle applies a batch of events and reports whether the read end is
// closed. Children go first: the kernel posts NOTE_DELETE on a file before
// NOTE_WRITE on its directory, and a rescan reads that to tell a removal from
// a rename away.
func (ino *inotifyInstance) handle(events []kqueueEvent) (closed bool) {
	var touched []*inotifyNode
	for idx := range events {
		event := &events[idx]
		if event.filter == kqFilterWrite {
			closed = closed || event.flags&kqEOF != 0
			continue
		}
		node := ino.nodes[int(event.ident)]
		if node == nil {
			continue
		}
		if node.notes == 0 {
			touched = append(touched, node)
		}
		node.notes |= event.fflags
	}

	var changed []*inotifyWatch
	for _, node := range touched {
		if node.name != "" {
			ino.childNotes(node)
		} else if node.isDir && node.notes&(noteWrite|noteLink|noteDelete) != 0 {
			changed = append(changed, node.watch)
		}
	}
	ino.rescan(changed)
	for _, node := range touched {
		if node.name == "" {
			ino.selfNotes(node)
		}
		node.notes = 0
	}
	return closed
}

func (ino *inotifyInstance) childNotes(node *inotifyNode) {
	if node.notes&noteDelete != 0 {
		node.unlinked = true
		return
	}
	if !node.isDir && node.notes&(noteWrite|noteExtend) != 0 {
		ino.modified(node.watch, node.name)
	}
	if node.notes&noteAttrib != 0 {
		ino.emit(node.watch, IN_ATTRIB|node.dirBit(), 0, node.name)
	}
}

func (ino *inotifyInstance) selfNotes(node *inotifyNode) {
	watch := node.watch
	if watch.gone {
		return
	}
	notes := node.notes
	if !node.isDir && notes&(noteWrite|noteExtend) != 0 {
		ino.modified(watch, "")
	}
	if notes&noteAttrib != 0 || !node.isDir && notes&noteLink != 0 {
		ino.emit(watch, IN_ATTRIB|node.dirBit(), 0, "")
	}
	if notes&noteRename != 0 {
		ino.emit(watch, IN_MOVE_SELF|node.dirBit(), 0, "")
	}
	switch {
	case notes&noteRevoke != 0:
		ino.queue.push(watch.wd, IN_UNMOUNT, 0, "")
	case notes&noteDelete != 0 && (node.isDir || !inotifyLinked(node.fd)):
		ino.emit(watch, IN_DELETE_SELF|node.dirBit(), 0, "")
	default:
		return
	}
	if !watch.gone {
		ino.dropWatch(watch)
		ino.queue.push(watch.wd, IN_IGNORED, 0, "")
	}
}

// inotifyLinked reports whether the file fd refers to still has a name.
func inotifyLinked(fd int) bool {
	var stat Stat_t
	return Fstat(fd, &stat) == nil && stat.Nlink > 0
}

// rescan compares the directories with what is recorded of them. A name that
// left and a name that arrived holding the same file are one rename. That
// name is within a directory or between some of them in this batch.
func (ino *inotifyInstance) rescan(watches []*inotifyWatch) {
	var departed []*inotifyNode
	var arrivals []inotifyArrival
	for _, watch := range watches {
		if !watch.gone {
			departed, arrivals = ino.compare(watch, departed, arrivals)
		}
	}

	sources := make(map[epollID]*inotifyNode, len(departed))
	for _, node := range departed {
		sources[node.id] = node
	}
	created := arrivals[:0]
	for _, arrival := range arrivals {
		id := epollID{arrival.watch.self.id.dev, arrival.entry.ino}
		node := sources[id]
		if node == nil || arrival.watch.gone {
			created = append(created, arrival)
			continue
		}
		delete(sources, id)
		ino.renamed(node, arrival)
	}

	for _, node := range departed {
		if node.moved {
			node.moved = false
			continue
		}
		// A rename onto an existing name replaces that file. Linux reports
		// the rename alone.
		if !node.watch.gone && node.watch.children[node.name] == nil {
			ino.removed(node)
		}
		ino.forget(node)
	}

	for _, arrival := range created {
		if arrival.watch.gone {
			continue
		}
		node, size, _ := ino.track(arrival.watch, arrival.entry)
		ino.emit(arrival.watch, IN_CREATE|node.dirBit(), 0, node.name)
		if size > 0 {
			ino.modified(arrival.watch, node.name)
		}
	}
}

// compare appends what left a directory and what arrived in it. A name now
// holding another file counts as both.
func (ino *inotifyInstance) compare(watch *inotifyWatch, departed []*inotifyNode, arrivals []inotifyArrival) ([]*inotifyNode, []inotifyArrival) {
	entries, ok := watch.readEntries()
	if !ok {
		return departed, arrivals
	}
	present := make(map[string]bool, len(entries))
	for _, entry := range entries {
		node := watch.children[entry.name]
		if node != nil && node.id.ino == entry.ino {
			present[entry.name] = true
			continue
		}
		arrivals = append(arrivals, inotifyArrival{watch, entry})
	}
	for name, node := range watch.children {
		if !present[name] {
			delete(watch.children, name)
			departed = append(departed, node)
		}
	}
	return departed, arrivals
}

func (ino *inotifyInstance) renamed(node *inotifyNode, arrival inotifyArrival) {
	ino.lastCookie++
	cookie := ino.lastCookie
	ino.emit(node.watch, IN_MOVED_FROM|node.dirBit(), cookie, node.name)
	node.moved = true
	node.watch, node.name = arrival.watch, arrival.entry.name
	node.watch.children[node.name] = node
	ino.emit(node.watch, IN_MOVED_TO|node.dirBit(), cookie, node.name)
}

// removed reports a name gone from a directory with no name here to pair it
// with. A file that still has a link went somewhere else.
func (ino *inotifyInstance) removed(node *inotifyNode) {
	if !node.unlinked && node.fd >= 0 && inotifyLinked(node.fd) {
		ino.lastCookie++
		ino.emit(node.watch, IN_MOVED_FROM|node.dirBit(), ino.lastCookie, node.name)
		return
	}
	ino.emit(node.watch, IN_DELETE|node.dirBit(), 0, node.name)
}
