// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// What a hint from a source means. A source says a name appeared, left or may
// have changed. A stat and the last stat of the name decide the events, on
// every host, so a host cannot report anything another host would not. That
// sets the events a caller can see: - IN_ACCESS, IN_OPEN and IN_CLOSE_NOWRITE
// never fire. Neither macOS nor NT tells an unprivileged process about them.
// - IN_CLOSE_WRITE fires with each IN_MODIFY. That IN_CLOSE_WRITE is for the
// same reason. - A rename seen at both ends is IN_MOVED_FROM and IN_MOVED_TO.
// A name arriving from an unwatched directory is IN_CREATE, and one leaving
// for such a directory is IN_DELETE. This is because NT cannot tell them
// apart., A name that arrives with content reports its write after its
// IN_CREATE. A name created and deleted between looks reports both. - A size
// or a write time that moved is a write. A mode or an owner that moved is
// IN_ATTRIB. A hint that changes neither reports nothing.

package syscall

// emit queues an event the watch's mask asks for. A oneshot watch ends here.
func (ino *inotifyInstance) emit(watch *inotifyWatch, mask, cookie uint32, name string) {
	if watch == nil || watch.gone || watch.mask&mask&IN_ALL_EVENTS == 0 {
		return
	}
	ino.queue.push(watch.wd, mask, cookie, name)
	if watch.mask&IN_ONESHOT != 0 {
		ino.dropWatch(watch)
		ino.queue.push(watch.wd, IN_IGNORED, 0, "")
	}
}

// written reports a write and the close that ends it.
func (ino *inotifyInstance) written(watch *inotifyWatch, name string) {
	ino.emit(watch, IN_MODIFY, 0, name)
	ino.emit(watch, IN_CLOSE_WRITE, 0, name)
}

// changed compares a stat with the last one. A directory's write time moves
// with its children, which is not an event on it.
func (ino *inotifyInstance) changed(watch *inotifyWatch, name string, entry, fresh *inotifyFile) {
	report := entry.regular && (entry.size != fresh.size || entry.mtime != fresh.mtime)
	attrib := entry.mode != fresh.mode || entry.uid != fresh.uid || entry.gid != fresh.gid
	bit := entry.dirBit()
	entry.size, entry.mtime = fresh.size, fresh.mtime
	entry.mode, entry.uid, entry.gid = fresh.mode, fresh.uid, fresh.gid
	if attrib {
		ino.emit(watch, IN_ATTRIB|bit, 0, name)
	}
	if report {
		ino.written(watch, name)
	}
}

// hold keeps a name that left, in case it is half of a rename.
func (ino *inotifyInstance) hold(entry *inotifyFile) {
	entry.waited = 0
	ino.held = append(ino.held, entry)
	ino.busy.Signal()
}

// heldNamed returns the held entry of a name.
func (ino *inotifyInstance) heldNamed(dir *inotifyDir, name string) *inotifyFile {
	for _, entry := range ino.held {
		if entry.dir == dir && entry.name == name {
			return entry
		}
	}
	return nil
}

func (ino *inotifyInstance) unhold(entry *inotifyFile) {
	for idx, held := range ino.held {
		if held == entry {
			ino.held = append(ino.held[:idx], ino.held[idx+1:]...)
			return
		}
	}
}

func (ino *inotifyInstance) takeHeld(id epollID) *inotifyFile {
	for _, entry := range ino.held {
		if entry.id == id {
			ino.unhold(entry)
			return entry
		}
	}
	return nil
}

// age reports the names that have waited long enough to have gone.
func (ino *inotifyInstance) age(millis uint32) {
	var kept, due []*inotifyFile
	for _, entry := range ino.held {
		entry.waited += millis
		if entry.waited < inotifyHoldMillis {
			kept = append(kept, entry)
		} else {
			due = append(due, entry)
		}
	}
	ino.held = kept
	for _, entry := range due {
		ino.departed(entry)
	}
}

// departed reports a name that has gone for good.
func (ino *inotifyInstance) departed(entry *inotifyFile) {
	ino.emit(entry.dir.watch, IN_DELETE|entry.dirBit(), 0, entry.name)
	watch := entry.dir.selves[entry.name]
	if watch != nil && watch.entry.id == entry.id {
		ino.selfGone(watch)
	}
}

// movedFrom finds a name that has left for dir before its leaving was heard.
func (ino *inotifyInstance) movedFrom(id epollID, dir *inotifyDir, name string) *inotifyFile {
	entry := ino.ids[id]
	if entry == nil {
		watch := ino.files[id]
		if watch == nil || watch.parent == nil {
			return nil
		}
		copied := watch.entry
		copied.dir, copied.name = watch.parent, watch.name
		entry = &copied
	}
	if entry.dir == dir && entry.name == name {
		return nil
	}
	if still := inotifyStat(entry.dir.join(entry.name)); still != nil && still.id == id {
		return nil
	}
	return entry
}

// pair reports one rename. from can belong to another directory.
func (ino *inotifyInstance) pair(from *inotifyFile, dir *inotifyDir, name string, to *inotifyFile) {
	ino.lastCookie++
	cookie := ino.lastCookie
	bit := to.dirBit()
	ino.emit(from.dir.watch, IN_MOVED_FROM|bit, cookie, from.name)
	ino.dropChild(from)
	if replaced := ino.heldNamed(dir, name); replaced != nil {
		ino.unhold(replaced)
	}
	if dir.children != nil {
		if existing := dir.children[name]; existing != nil {
			ino.dropChild(existing)
		}
		ino.setChild(dir, name, to)
	}
	ino.emit(dir.watch, IN_MOVED_TO|bit, cookie, name)
	watch := from.dir.selves[from.name]
	if watch != nil && watch.entry.id == from.id {
		ino.selfMoved(watch, dir, name)
	}
}

// selfMoved follows a watched name to where a rename took it.
func (ino *inotifyInstance) selfMoved(watch *inotifyWatch, dir *inotifyDir, name string) {
	ino.emit(watch, IN_MOVE_SELF|watch.entry.dirBit(), 0, "")
	if watch.gone {
		return
	}
	old := watch.parent
	delete(old.selves, watch.name)
	dir.selves[name] = watch
	watch.parent, watch.name = dir, name
	if watch.dir != nil {
		ino.rehome(watch.dir, dir.join(name))
	}
	if old != dir {
		ino.released(old)
	}
}

func (ino *inotifyInstance) selfGone(watch *inotifyWatch) {
	ino.emit(watch, IN_DELETE_SELF|watch.entry.dirBit(), 0, "")
	if !watch.gone {
		ino.dropWatch(watch)
		ino.queue.push(watch.wd, IN_IGNORED, 0, "")
	}
}

// added handles a name that has appeared.
func (ino *inotifyInstance) added(dir *inotifyDir, name string) {
	fresh := inotifyStat(dir.join(name))
	if fresh == nil {
		ino.addedAndGone(dir, name)
		return
	}
	from := ino.takeHeld(fresh.id)
	if from == nil {
		from = ino.movedFrom(fresh.id, dir, name)
	}
	if replaced := dir.selves[name]; replaced != nil && replaced.entry.id != fresh.id {
		ino.selfGone(replaced)
	}
	if from != nil {
		ino.pair(from, dir, name, fresh)
		return
	}
	if held := ino.heldNamed(dir, name); held != nil {
		ino.unhold(held)
		ino.departed(held)
	}
	existing := dir.children[name]
	if existing != nil && existing.id == fresh.id {
		ino.changed(dir.watch, name, existing, fresh)
		return
	}
	if existing != nil {
		ino.dropChild(existing)
		ino.departed(existing)
	}
	if dir.children == nil {
		return
	}
	ino.setChild(dir, name, fresh)
	ino.emit(dir.watch, IN_CREATE|fresh.dirBit(), 0, name)
	if fresh.regular && fresh.size > 0 {
		ino.written(dir.watch, name)
	}
}

// addedAndGone handles a name that went before it could be looked at. A child
// with no identity is what lets its removal report IN_DELETE.
func (ino *inotifyInstance) addedAndGone(dir *inotifyDir, name string) {
	if dir.children == nil {
		return
	}
	if existing := dir.children[name]; existing != nil {
		ino.dropChild(existing)
	}
	ino.setChild(dir, name, &inotifyFile{})
	ino.emit(dir.watch, IN_CREATE, 0, name)
}

// removed handles a name that has left. A name that may have left for another
// is held until the other half of a rename has had time to arrive.
func (ino *inotifyInstance) removed(dir *inotifyDir, name string, maybeRenamed bool) {
	entry := dir.children[name]
	watch := dir.selves[name]
	switch {
	case entry != nil:
		ino.dropChild(entry)
	case watch == nil || ino.heldNamed(dir, name) != nil:
		return
	default:
		copied := watch.entry
		copied.dir, copied.name = dir, name
		entry = &copied
	}
	if maybeRenamed {
		ino.hold(entry)
		return
	}
	ino.departed(entry)
}

// modifiedName handles a name that may have changed.
func (ino *inotifyInstance) modifiedName(dir *inotifyDir, name string) {
	child, watch := dir.children[name], dir.selves[name]
	if child == nil && watch == nil && dir.children == nil {
		return
	}
	fresh := inotifyStat(dir.join(name))
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

// selfChanged handles a directory that may have changed, for a host that
// reports that without naming it from its parent.
func (ino *inotifyInstance) selfChanged(dir *inotifyDir) {
	watch := dir.watch
	if watch == nil || watch.parent != nil {
		return
	}
	if fresh := inotifyStat(dir.path); fresh != nil {
		ino.changed(watch, "", &watch.entry, fresh)
	}
}

// lost handles a directory the host can say no more about.
func (ino *inotifyInstance) lost(dir *inotifyDir) {
	if dir.watch != nil {
		ino.selfGone(dir.watch)
	}
	for _, watch := range dir.selves {
		ino.selfGone(watch)
	}
}

// resync recovers from a change list too long for the host to keep, by
// comparing the directory with what is recorded of it.
func (ino *inotifyInstance) resync(dir *inotifyDir) {
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
				ino.removed(dir, name, true)
			}
		}
	}
	for name := range dir.selves {
		if inotifyStat(dir.join(name)) == nil {
			ino.removed(dir, name, true)
		} else {
			ino.modifiedName(dir, name)
		}
	}
}
