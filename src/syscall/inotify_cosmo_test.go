// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// inotifyEvent is one decoded record.
type inotifyEvent struct {
	wd     int32
	mask   uint32
	cookie uint32
	name   string
}

// inotifyFile reads an inotify descriptor the way fsnotify does, through the
// runtime poller.
type inotifyFile struct {
	t       *testing.T
	file    *os.File
	fd      int
	pending []inotifyEvent
}

// newInotify makes a nonblocking instance. Asking for the temporary directory
// first orders its removal after the instance's close, because NT cannot remove
// a directory while a watch holds it open.
func newInotify(t *testing.T) *inotifyFile {
	t.Helper()
	t.TempDir()
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		t.Fatalf("InotifyInit1: %v", err)
	}
	file := os.NewFile(uintptr(fd), "inotify")
	t.Cleanup(func() { closeInotify(t, fd, file.Close) })
	return &inotifyFile{t: t, file: file, fd: fd}
}

// closeInotify closes an instance and waits for it to let go of its handles.
func closeInotify(t *testing.T, fd int, closer func() error) {
	t.Helper()
	done := syscall.InotifyDoneForTest(fd)
	if err := closer(); err != nil {
		t.Errorf("close: %v", err)
	}
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Errorf("instance %d never let go of its handles", fd)
	}
}

func decodeInotify(buf []byte) []inotifyEvent {
	var events []inotifyEvent
	for len(buf) >= syscall.SizeofInotifyEvent {
		length := binary.NativeEndian.Uint32(buf[12:])
		end := syscall.SizeofInotifyEvent + int(length)
		name := string(buf[syscall.SizeofInotifyEvent:end])
		events = append(events, inotifyEvent{
			wd:     int32(binary.NativeEndian.Uint32(buf[0:])),
			mask:   binary.NativeEndian.Uint32(buf[4:]),
			cookie: binary.NativeEndian.Uint32(buf[8:]),
			name:   strings.TrimRight(name, "\x00"),
		})
		buf = buf[end:]
	}
	return events
}

// next returns the next event, or false once nothing arrives within limit.
func (ino *inotifyFile) next(limit time.Duration) (inotifyEvent, bool) {
	ino.t.Helper()
	if len(ino.pending) == 0 {
		if err := ino.file.SetReadDeadline(time.Now().Add(limit)); err != nil {
			ino.t.Fatalf("SetReadDeadline: %v", err)
		}
		buf := make([]byte, 65536)
		count, err := ino.file.Read(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return inotifyEvent{}, false
		}
		if err != nil {
			ino.t.Fatalf("read: %v", err)
		}
		ino.pending = decodeInotify(buf[:count])
	}
	event := ino.pending[0]
	ino.pending = ino.pending[1:]
	return event, true
}

// await reads until an event with this mask and name arrives.
func (ino *inotifyFile) await(mask uint32, name string) inotifyEvent {
	ino.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var seen []inotifyEvent
	for time.Now().Before(deadline) {
		event, ok := ino.next(time.Until(deadline))
		if !ok {
			break
		}
		if event.mask == mask && event.name == name {
			return event
		}
		seen = append(seen, event)
	}
	ino.t.Fatalf("no event %#x %q, saw %+v", mask, name, seen)
	return inotifyEvent{}
}

// settle collects what arrives until the watcher has been quiet for a moment.
func (ino *inotifyFile) settle() []inotifyEvent {
	ino.t.Helper()
	var events []inotifyEvent
	for {
		event, ok := ino.next(300 * time.Millisecond)
		if !ok {
			return events
		}
		events = append(events, event)
	}
}

func (ino *inotifyFile) add(path string, mask uint32) int {
	ino.t.Helper()
	wd, err := syscall.InotifyAddWatch(ino.fd, path, mask)
	if err != nil {
		ino.t.Fatalf("InotifyAddWatch(%q, %#x): %v", path, mask, err)
	}
	return wd
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInotifyCreateAndWrite(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	wd := ino.add(dir, syscall.IN_CREATE|syscall.IN_MODIFY|syscall.IN_CLOSE_WRITE)

	file, err := os.Create(filepath.Join(dir, "created"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if event := ino.await(syscall.IN_CREATE, "created"); event.wd != int32(wd) {
		t.Errorf("create carried wd %d, want %d", event.wd, wd)
	}
	if _, err := file.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	if events := ino.settle(); len(events) != 0 {
		t.Errorf("a write on an open descriptor reported %+v", events)
	}
	file.Close()
	ino.await(syscall.IN_MODIFY, "created")
	ino.await(syscall.IN_CLOSE_WRITE, "created")
}

// TestInotifyTruncate has no descriptor to wait for, in either direction.
func TestInotifyTruncate(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	writeFile(t, path, "text")
	ino.add(dir, syscall.IN_MODIFY)

	if err := os.Truncate(path, 2); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_MODIFY, "file")
	if err := os.Truncate(path, 8); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_MODIFY, "file")
}

// TestInotifyNeverFires reads, opens and closes what the mask asks about.
func TestInotifyNeverFires(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	writeFile(t, path, "text")
	ino.add(dir, syscall.IN_ACCESS|syscall.IN_OPEN|syscall.IN_CLOSE_NOWRITE)
	ino.add(path, syscall.IN_ACCESS|syscall.IN_OPEN|syscall.IN_CLOSE_NOWRITE)

	if _, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadDir(dir); err != nil {
		t.Fatal(err)
	}
	if events := ino.settle(); len(events) != 0 {
		t.Errorf("reading reported %+v", events)
	}
}

// TestInotifyMovedInIsCreate brings a file from an unwatched directory.
func TestInotifyMovedInIsCreate(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "file")
	writeFile(t, elsewhere, "text")
	ino.add(dir, syscall.IN_CREATE|syscall.IN_MODIFY|syscall.IN_CLOSE_WRITE|syscall.IN_MOVE)

	if err := os.Rename(elsewhere, filepath.Join(dir, "file")); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_CREATE, "file")
	ino.await(syscall.IN_MODIFY, "file")
	ino.await(syscall.IN_CLOSE_WRITE, "file")
	if events := ino.settle(); len(events) != 0 {
		t.Errorf("after the arrival, saw %+v", events)
	}
}

// TestInotifyMovedOutIsDelete sends a file to an unwatched directory.
func TestInotifyMovedOutIsDelete(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "file"), "text")
	ino.add(dir, syscall.IN_DELETE|syscall.IN_MOVE)

	if err := os.Rename(filepath.Join(dir, "file"), filepath.Join(t.TempDir(), "file")); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_DELETE, "file")
	if events := ino.settle(); len(events) != 0 {
		t.Errorf("after the departure, saw %+v", events)
	}
}

// TestInotifyShortLivedName creates and deletes faster than a host looks.
func TestInotifyShortLivedName(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	ino.add(dir, syscall.IN_CREATE|syscall.IN_DELETE)
	path := filepath.Join(dir, "brief")
	for range 20 {
		writeFile(t, path, "")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		ino.await(syscall.IN_CREATE, "brief")
		ino.await(syscall.IN_DELETE, "brief")
	}
}

// TestInotifySymlinkNoFollow watches a link, not what it names.
func TestInotifySymlinkNoFollow(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	writeFile(t, target, "text")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	wd := ino.add(link, syscall.IN_MODIFY|syscall.IN_DELETE_SELF|syscall.IN_DONT_FOLLOW)

	writeFile(t, target, "changed")
	if events := ino.settle(); len(events) != 0 {
		t.Errorf("a write through the link's target reported %+v", events)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_DELETE_SELF, "")
	if event := ino.await(syscall.IN_IGNORED, ""); event.wd != int32(wd) {
		t.Errorf("IN_IGNORED carried wd %d, want %d", event.wd, wd)
	}
}

// TestInotifyReadSizes checks that a read returns whole records, and refuses a
// buffer the next one does not fit.
func TestInotifyReadSizes(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	ino.add(dir, syscall.IN_CREATE)
	writeFile(t, filepath.Join(dir, "first"), "")
	writeFile(t, filepath.Join(dir, "second"), "")
	time.Sleep(200 * time.Millisecond)

	var small [syscall.SizeofInotifyEvent]byte
	if _, err := syscall.Read(ino.fd, small[:]); err != syscall.EINVAL {
		t.Fatalf("a read too small for a record = %v, want EINVAL", err)
	}
	buf := make([]byte, syscall.SizeofInotifyEvent+16)
	count, err := syscall.Read(ino.fd, buf)
	if err != nil {
		t.Fatalf("a read that fits one record: %v", err)
	}
	if events := decodeInotify(buf[:count]); len(events) != 1 || events[0].name != "first" {
		t.Errorf("a read that fits one record returned %+v", events)
	}
	count, err = syscall.Read(ino.fd, buf)
	if err != nil {
		t.Fatalf("the read after that: %v", err)
	}
	if events := decodeInotify(buf[:count]); len(events) != 1 || events[0].name != "second" {
		t.Errorf("the read after that returned %+v", events)
	}
}

// TestInotifyDescriptorsPerChild checks that a directory's children cost no
// descriptors, which is what kept a large tree under its descriptor limit.
func TestInotifyDescriptorsPerChild(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	const children = 256
	for idx := range children {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("child-%03d", idx)), "")
	}
	before := lowestFreeFd(t, dir)
	ino.add(dir, syscall.IN_ALL_EVENTS)
	if after := lowestFreeFd(t, dir); after-before >= children/2 {
		t.Errorf("a watch took the lowest free descriptor from %d to %d", before, after)
	}
}

func lowestFreeFd(t *testing.T, path string) int {
	t.Helper()
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	syscall.Close(fd)
	return fd
}

func TestInotifyExistingChildWrite(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "present")
	writeFile(t, path, "before")
	ino.add(dir, syscall.IN_MODIFY|syscall.IN_ATTRIB)

	writeFile(t, path, "after")
	ino.await(syscall.IN_MODIFY, "present")
	// The write bit is the only part of a mode NT has.
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_ATTRIB, "present")
}

func TestInotifyDelete(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "file"), "text")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	ino.add(dir, syscall.IN_DELETE)

	if err := os.Remove(filepath.Join(dir, "file")); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_DELETE, "file")
	if err := os.Remove(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_DELETE|syscall.IN_ISDIR, "sub")
}

func TestInotifySubdirectoryCreate(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	ino.add(dir, syscall.IN_CREATE)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_CREATE|syscall.IN_ISDIR, "sub")
}

func TestInotifyRenamePairsCookies(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "before"), "text")
	ino.add(dir, syscall.IN_MOVE|syscall.IN_CREATE|syscall.IN_DELETE)

	if err := os.Rename(filepath.Join(dir, "before"), filepath.Join(dir, "after")); err != nil {
		t.Fatal(err)
	}
	from := ino.await(syscall.IN_MOVED_FROM, "before")
	to := ino.await(syscall.IN_MOVED_TO, "after")
	if from.cookie == 0 || from.cookie != to.cookie {
		t.Errorf("rename cookies %d and %d do not pair", from.cookie, to.cookie)
	}
}

func TestInotifyRenameBetweenDirectories(t *testing.T) {
	ino := newInotify(t)
	first, second := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(first, "file"), "text")
	ino.add(first, syscall.IN_MOVE)
	ino.add(second, syscall.IN_MOVE)

	if err := os.Rename(filepath.Join(first, "file"), filepath.Join(second, "file")); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_MOVED_FROM, "file")
	ino.await(syscall.IN_MOVED_TO, "file")
}

// TestInotifyRenameOverKeepsWriteWatch is how editors save: a temporary file
// replaces the original, and writes after that must still be seen.
func TestInotifyRenameOverKeepsWriteWatch(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	writeFile(t, target, "old")
	writeFile(t, filepath.Join(dir, "scratch"), "new")
	ino.add(dir, syscall.IN_MOVE|syscall.IN_DELETE|syscall.IN_MODIFY)

	if err := os.Rename(filepath.Join(dir, "scratch"), target); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_MOVED_TO, "target")
	for _, event := range ino.settle() {
		if event.mask&syscall.IN_DELETE != 0 {
			t.Errorf("a rename over a file reported %+v", event)
		}
	}
	writeFile(t, target, "newer")
	ino.await(syscall.IN_MODIFY, "target")
}

func TestInotifyWatchedFile(t *testing.T) {
	ino := newInotify(t)
	path := filepath.Join(t.TempDir(), "file")
	writeFile(t, path, "before")
	wd := ino.add(path, syscall.IN_MODIFY|syscall.IN_ATTRIB|syscall.IN_DELETE_SELF)

	writeFile(t, path, "after")
	ino.await(syscall.IN_MODIFY, "")
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_ATTRIB, "")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_DELETE_SELF, "")
	if event := ino.await(syscall.IN_IGNORED, ""); event.wd != int32(wd) {
		t.Errorf("IN_IGNORED carried wd %d, want %d", event.wd, wd)
	}
}

func TestInotifyMoveSelf(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	writeFile(t, path, "text")
	ino.add(path, syscall.IN_MOVE_SELF)
	if err := os.Rename(path, filepath.Join(dir, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_MOVE_SELF, "")
}

func TestInotifyDirectoryDeleteSelf(t *testing.T) {
	ino := newInotify(t)
	dir := filepath.Join(t.TempDir(), "sub")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ino.add(dir, syscall.IN_DELETE_SELF)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	event, ok := ino.next(10 * time.Second)
	if !ok || event.mask&syscall.IN_DELETE_SELF == 0 {
		t.Fatalf("after rmdir, got %+v, %v", event, ok)
	}
	ino.await(syscall.IN_IGNORED, "")
}

func TestInotifyRmWatch(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	wd := ino.add(dir, syscall.IN_CREATE)
	if _, err := syscall.InotifyRmWatch(ino.fd, uint32(wd)); err != nil {
		t.Fatalf("InotifyRmWatch: %v", err)
	}
	ino.await(syscall.IN_IGNORED, "")
	if _, err := syscall.InotifyRmWatch(ino.fd, uint32(wd)); err != syscall.EINVAL {
		t.Errorf("second InotifyRmWatch = %v, want EINVAL", err)
	}
	writeFile(t, filepath.Join(dir, "unwatched"), "text")
	if events := ino.settle(); len(events) != 0 {
		t.Errorf("a removed watch reported %+v", events)
	}
}

func TestInotifySamePathSharesWatch(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	first := ino.add(dir, syscall.IN_CREATE)
	second := ino.add(dir, syscall.IN_DELETE|syscall.IN_MASK_ADD)
	if first != second {
		t.Fatalf("second add made wd %d, want %d", second, first)
	}
	if _, err := syscall.InotifyAddWatch(ino.fd, dir, syscall.IN_CREATE|0x10000000); err != syscall.EEXIST {
		t.Errorf("IN_MASK_CREATE on a watched path = %v, want EEXIST", err)
	}
	path := filepath.Join(dir, "file")
	writeFile(t, path, "text")
	ino.await(syscall.IN_CREATE, "file")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ino.await(syscall.IN_DELETE, "file")
}

func TestInotifyAddWatchErrors(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	writeFile(t, path, "text")

	if _, err := syscall.InotifyAddWatch(ino.fd, dir, 0); err != syscall.EINVAL {
		t.Errorf("empty mask = %v, want EINVAL", err)
	}
	if _, err := syscall.InotifyAddWatch(ino.fd, dir, syscall.IN_ONESHOT); err != nil {
		t.Errorf("IN_ONESHOT alone = %v, want a watch", err)
	}
	if _, err := syscall.InotifyAddWatch(ino.fd, dir, syscall.IN_CREATE|syscall.IN_MASK_ADD|0x10000000); err != syscall.EINVAL {
		t.Errorf("IN_MASK_ADD with IN_MASK_CREATE = %v, want EINVAL", err)
	}
	if _, err := syscall.InotifyAddWatch(ino.fd, filepath.Join(dir, "missing"), syscall.IN_CREATE); err != syscall.ENOENT {
		t.Errorf("missing path = %v, want ENOENT", err)
	}
	if _, err := syscall.InotifyAddWatch(ino.fd, path, syscall.IN_CREATE|syscall.IN_ONLYDIR); err != syscall.ENOTDIR {
		t.Errorf("IN_ONLYDIR on a file = %v, want ENOTDIR", err)
	}
	if _, err := syscall.InotifyAddWatch(int(os.Stdin.Fd()), dir, syscall.IN_CREATE); err != syscall.EINVAL {
		t.Errorf("descriptor that is no instance = %v, want EINVAL", err)
	}
}

func TestInotifyInitFlags(t *testing.T) {
	fd, err := syscall.InotifyInit1(0x1)
	if err != syscall.EINVAL {
		t.Errorf("InotifyInit1 with an unknown flag = %d, %v, want EINVAL", fd, err)
	}
}

func TestInotifyEmptyNonblockingRead(t *testing.T) {
	ino := newInotify(t)
	var buf [64]byte
	if _, err := syscall.Read(ino.fd, buf[:]); err != syscall.EAGAIN {
		t.Errorf("read of an empty instance = %v, want EAGAIN", err)
	}
}

// TestInotifyBlockingRead has no IN_NONBLOCK, so a read waits for an event.
func TestInotifyBlockingRead(t *testing.T) {
	dir := t.TempDir()
	fd, err := syscall.InotifyInit()
	if err != nil {
		t.Fatalf("InotifyInit: %v", err)
	}
	defer closeInotify(t, fd, func() error { return syscall.Close(fd) })
	if _, err := syscall.InotifyAddWatch(fd, dir, syscall.IN_CREATE); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		count, err := syscall.Read(fd, buf)
		if err != nil {
			done <- err.Error()
			return
		}
		done <- decodeInotify(buf[:count])[0].name
	}()
	writeFile(t, filepath.Join(dir, "file"), "text")
	select {
	case name := <-done:
		if name != "file" {
			t.Errorf("read reported %q", name)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocking read never returned")
	}
}

// TestInotifyThroughRawSyscall takes the route x/sys/unix does, past the
// exported wrappers.
func TestInotifyThroughRawSyscall(t *testing.T) {
	path, err := syscall.BytePtrFromString(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fd, _, errno := syscall.RawSyscall(syscall.SYS_INOTIFY_INIT1, syscall.IN_CLOEXEC|syscall.IN_NONBLOCK, 0, 0)
	if errno != 0 {
		t.Fatalf("inotify_init1: %v", errno)
	}
	defer closeInotify(t, int(fd), func() error { return syscall.Close(int(fd)) })
	if err != nil {
		t.Fatal(err)
	}
	wd, _, errno := syscall.Syscall(syscall.SYS_INOTIFY_ADD_WATCH, fd, uintptr(unsafe.Pointer(path)), syscall.IN_CREATE)
	if errno != 0 {
		t.Fatalf("inotify_add_watch: %v", errno)
	}
	if _, _, errno = syscall.RawSyscall(syscall.SYS_INOTIFY_RM_WATCH, fd, wd, 0); errno != 0 {
		t.Fatalf("inotify_rm_watch: %v", errno)
	}
}

// TestInotifyManyChildren has more children than one read of the directory
// returns.
func TestInotifyManyChildren(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	const children = 300
	for idx := range children {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("child-%03d", idx)), "text")
	}
	ino.add(dir, syscall.IN_MODIFY)
	for idx := range children {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("child-%03d", idx)), "changed")
	}
	seen := map[string]bool{}
	deadline := time.Now().Add(20 * time.Second)
	for len(seen) < children && time.Now().Before(deadline) {
		event, ok := ino.next(time.Until(deadline))
		if !ok {
			break
		}
		seen[event.name] = true
	}
	if len(seen) != children {
		t.Fatalf("saw %d of %d children", len(seen), children)
	}
}

// TestInotifyBacklog makes more events than a pipe holds before reading any.
// Nothing may be lost once the reader starts draining.
func TestInotifyBacklog(t *testing.T) {
	ino := newInotify(t)
	dir := t.TempDir()
	ino.add(dir, syscall.IN_CREATE)
	const files = 1500
	prefix := strings.Repeat("x", 200)
	for idx := range files {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("%s-%04d", prefix, idx)), "")
	}
	time.Sleep(200 * time.Millisecond)
	seen := map[string]bool{}
	deadline := time.Now().Add(20 * time.Second)
	for len(seen) < files && time.Now().Before(deadline) {
		event, ok := ino.next(time.Until(deadline))
		if !ok {
			break
		}
		seen[event.name] = true
	}
	if len(seen) != files {
		t.Fatalf("saw %d of %d files", len(seen), files)
	}
}

// TestInotifyCloseReleasesInstance checks that the goroutine and its
// descriptors go away when the caller closes the instance.
func TestInotifyCloseReleasesInstance(t *testing.T) {
	awaitInotifyLive(t, 0)
	dir := t.TempDir()
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syscall.InotifyAddWatch(fd, dir, syscall.IN_CREATE); err != nil {
		t.Fatal(err)
	}
	if live := syscall.InotifyLiveForTest(); live != 1 {
		t.Fatalf("%d instances live, want 1", live)
	}
	closeInotify(t, fd, func() error { return syscall.Close(fd) })
	awaitInotifyLive(t, 0)
}

// awaitInotifyLive waits for closed instances to finish going away. Earlier
// tests' instances count until their goroutines have seen the close.
func awaitInotifyLive(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for syscall.InotifyLiveForTest() != want {
		if time.Now().After(deadline) {
			t.Fatalf("%d instances live, want %d", syscall.InotifyLiveForTest(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestInotifyQueueOverflow drives the queue directly, on every host, because
// a kernel event costs far more than a test can spend reaching its limit.
func TestInotifyQueueOverflow(t *testing.T) {
	var queue syscall.InotifyQueueForTest
	for idx := range syscall.InotifyQueueMaxForTest + 10 {
		queue.PushForTest(1, syscall.IN_MODIFY, uint32(idx), "file")
	}
	records := queue.RecordsForTest()
	if len(records) != syscall.InotifyQueueMaxForTest+1 {
		t.Fatalf("queue holds %d records, want %d", len(records), syscall.InotifyQueueMaxForTest+1)
	}
	last := decodeInotify(records[len(records)-1])[0]
	if last.mask != syscall.IN_Q_OVERFLOW || last.wd != -1 {
		t.Errorf("last record = %+v, want IN_Q_OVERFLOW on wd -1", last)
	}
}

func TestInotifyQueueMergesRepeats(t *testing.T) {
	var queue syscall.InotifyQueueForTest
	queue.PushForTest(1, syscall.IN_MODIFY, 0, "file")
	queue.PushForTest(1, syscall.IN_MODIFY, 0, "file")
	queue.PushForTest(1, syscall.IN_MODIFY, 0, "other")
	records := queue.RecordsForTest()
	if len(records) != 2 {
		t.Fatalf("queue holds %d records, want 2", len(records))
	}
	if event := decodeInotify(records[0])[0]; event.name != "file" || event.wd != 1 {
		t.Errorf("first record = %+v", event)
	}
	if len(records[0])%syscall.SizeofInotifyEvent != 0 {
		t.Errorf("record of %d bytes is not padded", len(records[0]))
	}
}
