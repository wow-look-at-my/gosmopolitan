package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// File-metadata and system-information syscalls. Every one of these was
// ENOSYS on macOS until the darwin emulation grew it, and none of them
// is reached by the rest of the probe - os.File.Sync, os.Truncate,
// os.Chmod, os.Chtimes, os.Link, os.Symlink, statfs, uname, getrlimit
// and the credential getters all bottom out in a syscall the fast path
// does not carry. So this is the check that keeps them working.
//
// On a Windows host the NT emulation has not brought this surface up.
// Following checkDupFile, the block then reports what each step
// answered instead of failing: the log records the gap, and the day NT
// grows one of these the line is already there.

// softStep runs one step of a check. On a host where the surface is
// expected to work, an error fails the check; elsewhere it is recorded.
type softStep struct {
	name  string
	soft  bool
	notes []string
	bad   bool
}

func (s *softStep) do(what string, err error) bool {
	if err == nil {
		return true
	}
	if s.soft {
		s.notes = append(s.notes, what+": "+err.Error())
		return false
	}
	fail(s.name, "%s: %v", what, err)
	s.bad = true
	return false
}

// finish prints the single verdict line the output contract requires.
func (s *softStep) finish(detail string) {
	if s.bad {
		return // do() already printed the FAIL
	}
	if len(s.notes) > 0 {
		ok(s.name, fmt.Sprintf("%s: unsupported [%s]", cosmoHostOS(), strings.Join(s.notes, "; ")))
		return
	}
	ok(s.name, detail)
}

func checkFsMeta() {
	s := &softStep{name: "fsmeta", soft: cosmoHostOS() == "windows"}

	dir, err := os.MkdirTemp("", "rp-fsmeta")
	if err != nil {
		fail("fsmeta", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "f")
	f, err := os.Create(path)
	if err != nil {
		fail("fsmeta", "create: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write([]byte("0123456789ab")); err != nil {
		fail("fsmeta", "write: %v", err)
		return
	}

	s.do("Sync", f.Sync())

	// ftruncate then truncate: two different syscalls, so both sizes are
	// checked rather than only the last one.
	if s.do("File.Truncate", f.Truncate(5)) {
		if fi, err := f.Stat(); err == nil && fi.Size() != 5 {
			s.do("File.Truncate size", fmt.Errorf("size %d, want 5", fi.Size()))
		}
	}
	if s.do("Truncate", os.Truncate(path, 3)) {
		if fi, err := os.Stat(path); err == nil && fi.Size() != 3 {
			s.do("Truncate size", fmt.Errorf("size %d, want 3", fi.Size()))
		}
	}

	// fchmod then fchmodat. Permission bits have the same values on
	// every host, so the mode read back is compared exactly.
	if s.do("File.Chmod", f.Chmod(0o640)) {
		if fi, err := f.Stat(); err == nil && fi.Mode().Perm() != 0o640 {
			s.do("File.Chmod mode", fmt.Errorf("mode %v, want -rw-r-----", fi.Mode().Perm()))
		}
	}
	if s.do("Chmod", os.Chmod(path, 0o600)) {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm() != 0o600 {
			s.do("Chmod mode", fmt.Errorf("mode %v, want -rw-------", fi.Mode().Perm()))
		}
	}

	// fchownat with both ids -1 changes nothing, so it needs no
	// privilege and still proves the call reaches the kernel.
	s.do("Chown", os.Chown(path, -1, -1))

	// utimensat, twice: once with both stamps, once with a zero atime,
	// which os.Chtimes turns into the UTIME_OMIT sentinel the emulation
	// has to rewrite for Apple.
	want := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if s.do("Chtimes", os.Chtimes(path, want, want)) {
		if fi, err := os.Stat(path); err == nil && !fi.ModTime().Equal(want) {
			s.do("Chtimes mtime", fmt.Errorf("mtime %v, want %v", fi.ModTime(), want))
		}
	}
	want2 := want.Add(time.Hour)
	if s.do("Chtimes omit-atime", os.Chtimes(path, time.Time{}, want2)) {
		if fi, err := os.Stat(path); err == nil && !fi.ModTime().Equal(want2) {
			s.do("Chtimes omit-atime mtime", fmt.Errorf("mtime %v, want %v", fi.ModTime(), want2))
		}
	}

	// linkat and symlinkat.
	link := filepath.Join(dir, "hard")
	if s.do("Link", os.Link(path, link)) {
		if fi, err := os.Stat(link); err == nil && fi.Size() != 3 {
			s.do("Link size", fmt.Errorf("size %d, want 3", fi.Size()))
		}
	}
	sym := filepath.Join(dir, "sym")
	if s.do("Symlink", os.Symlink(path, sym)) {
		if got, err := os.Readlink(sym); err == nil && got != path {
			s.do("Readlink", fmt.Errorf("target %q, want %q", got, path))
		}
	}

	// mknodat. Apple has no directory-relative mknod, so the emulation
	// serves this only for AT_FDCWD - which is what Mkfifo passes.
	fifo := filepath.Join(dir, "fifo")
	if s.do("Mkfifo", syscall.Mkfifo(fifo, 0o600)) {
		if fi, err := os.Lstat(fifo); err == nil && fi.Mode()&os.ModeNamedPipe == 0 {
			s.do("Mkfifo mode", fmt.Errorf("mode %v, want a named pipe", fi.Mode()))
		}
	}

	// fchdir. The working directory is process-wide and checkWd asserts
	// against it, so the original is restored before returning.
	if wd, err := os.Getwd(); err == nil {
		if d, err := os.Open(dir); err == nil {
			s.do("Fchdir", syscall.Fchdir(int(d.Fd())))
			d.Close()
			if err := os.Chdir(wd); err != nil {
				fail("fsmeta", "restore wd %q: %v", wd, err)
				return
			}
		}
	}

	s.finish("sync/truncate/chmod/chown/chtimes/link/symlink/mkfifo/fchdir")
}

func checkSysInfo() {
	s := &softStep{name: "sysinfo", soft: cosmoHostOS() == "windows"}
	var detail []string

	// statfs and fstatfs. Both fill the same struct, so the block size
	// they report for one file must agree.
	var sfs syscall.Statfs_t
	if s.do("Statfs", syscall.Statfs(os.TempDir(), &sfs)) {
		if sfs.Bsize <= 0 || sfs.Blocks == 0 {
			s.do("Statfs values", fmt.Errorf("bsize %d blocks %d, want both positive", sfs.Bsize, sfs.Blocks))
		} else {
			detail = append(detail, fmt.Sprintf("bsize=%d", sfs.Bsize))
		}
	}
	if f, err := os.Open(os.TempDir()); err == nil {
		var ffs syscall.Statfs_t
		if s.do("Fstatfs", syscall.Fstatfs(int(f.Fd()), &ffs)) && ffs.Bsize != sfs.Bsize {
			s.do("Fstatfs bsize", fmt.Errorf("fstatfs %d != statfs %d for the same filesystem", ffs.Bsize, sfs.Bsize))
		}
		f.Close()
	}

	// uname. Sysname is the field every host fills, and the emulation
	// has to copy it out of a 256-byte Apple field into a 65-byte one.
	var un syscall.Utsname
	if s.do("Uname", syscall.Uname(&un)) {
		sys := cstr(un.Sysname[:])
		if sys == "" {
			s.do("Uname sysname", fmt.Errorf("empty"))
		} else {
			detail = append(detail, "uname="+sys)
		}
	}

	// prlimit64. RLIMIT_NOFILE is the one the runtime itself raises at
	// startup, so a wrong resource number here would be visible.
	var rl syscall.Rlimit
	if s.do("Getrlimit", syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl)) {
		if rl.Cur == 0 || rl.Cur > rl.Max {
			s.do("Getrlimit values", fmt.Errorf("cur %d max %d", rl.Cur, rl.Max))
		} else {
			detail = append(detail, fmt.Sprintf("nofile=%d/%d", rl.Cur, rl.Max))
		}
	}

	// getpriority. The syscall reports 20-nice, so a default-priority
	// process reads 20 and the legal range is 1..40. A raw Apple nice
	// value would fall outside it.
	if prio, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0); s.do("Getpriority", err) {
		if prio < 1 || prio > 40 {
			s.do("Getpriority value", fmt.Errorf("%d, want the 1..40 the Linux syscall reports", prio))
		} else {
			detail = append(detail, fmt.Sprintf("prio=%d", prio))
		}
	}

	// getgroups. An empty list is legal; an error is not.
	if _, err := syscall.Getgroups(); s.do("Getgroups", err) {
	}

	// getpgid. Getpgrp is the same syscall with pid 0, so the two must
	// agree - and a silently discarded error would show up as a zero.
	if pgid, err := syscall.Getpgid(0); s.do("Getpgid", err) {
		if pgid <= 0 {
			s.do("Getpgid value", fmt.Errorf("%d, want a positive process-group id", pgid))
		} else if grp := syscall.Getpgrp(); grp != pgid {
			s.do("Getpgrp", fmt.Errorf("Getpgrp %d != Getpgid(0) %d", grp, pgid))
		} else {
			detail = append(detail, fmt.Sprintf("pgid=%d", pgid))
		}
	}

	s.finish(strings.Join(detail, " "))
}

// cstr reads a NUL-terminated field out of a fixed-size buffer.
func cstr(b []byte) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

// checkSendfile exercises the sendfile syscall directly. Nothing in the
// standard library reaches it on cosmo - internal/poll's sendfile file
// carries no cosmo build tag - so a caller that uses syscall.Sendfile is
// the only thing that would ever find it broken.
//
// The destination is a socketpair end because Apple's sendfile refuses
// anything else, and the payload is small enough to fit in the socket
// buffer so the write cannot block.
func checkSendfile() {
	s := &softStep{name: "sendfile", soft: cosmoHostOS() == "windows"}

	dir, err := os.MkdirTemp("", "rp-sendfile")
	if err != nil {
		fail("sendfile", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	const payload = "sendfile-payload"
	path := filepath.Join(dir, "src")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		fail("sendfile", "write: %v", err)
		return
	}
	src, err := os.Open(path)
	if err != nil {
		fail("sendfile", "open: %v", err)
		return
	}
	defer src.Close()

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		fail("sendfile", "socketpair: %v", err)
		return
	}
	defer syscall.Close(fds[0])
	defer syscall.Close(fds[1])

	// With an explicit offset the file's own offset must not move, and
	// the offset variable must advance by what was sent.
	off := int64(0)
	n, err := syscall.Sendfile(fds[0], int(src.Fd()), &off, len(payload))
	if !s.do("Sendfile", err) {
		s.finish("")
		return
	}
	if n != len(payload) || off != int64(len(payload)) {
		s.do("Sendfile counts", fmt.Errorf("sent %d off %d, want %d and %d", n, off, len(payload), len(payload)))
	}
	buf := make([]byte, len(payload))
	if _, err := syscall.Read(fds[1], buf); s.do("read back", err) && string(buf) != payload {
		s.do("payload", fmt.Errorf("got %q, want %q", buf, payload))
	}
	if cur, err := src.Seek(0, 1); err == nil && cur != 0 {
		s.do("file offset", fmt.Errorf("offset moved to %d; an explicit offset must leave it alone", cur))
	}

	// With no offset the file's own offset is the starting point and it
	// must advance instead.
	n, err = syscall.Sendfile(fds[0], int(src.Fd()), nil, len(payload))
	if s.do("Sendfile nil-offset", err) {
		if n != len(payload) {
			s.do("Sendfile nil-offset count", fmt.Errorf("sent %d, want %d", n, len(payload)))
		}
		if cur, err := src.Seek(0, 1); err == nil && cur != int64(len(payload)) {
			s.do("file offset advance", fmt.Errorf("offset %d, want %d", cur, len(payload)))
		}
		if _, err := syscall.Read(fds[1], buf); err == nil && string(buf) != payload {
			s.do("nil-offset payload", fmt.Errorf("got %q, want %q", buf, payload))
		}
	}

	s.finish(fmt.Sprintf("%d bytes, both offset modes", len(payload)))
}
