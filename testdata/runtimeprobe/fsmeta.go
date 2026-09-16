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
// The split across three checks is by HOST SUPPORT, so that each one
// can be a hard assertion everywhere it runs:
//
//   - fsmeta covers what every host serves, Windows included.
//   - fslinks covers symlinks and the owner's write bit, which every
//     host serves too.
//   - fsmetaunix covers what NT has no counterpart for (unix ownership,
//     the other permission bits, FIFOs).
//   - sysinfo covers statfs/uname/rlimit/priority/credentials, which
//     upstream's own windows port does not expose either.
//
// The last two follow checkDupFile on a Windows host: they report what
// each step answered instead of failing, so the log records the gap and
// the day NT grows one the line is already there.

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

// checkFsMeta covers the metadata syscalls every host serves. It is a
// hard assertion everywhere, Windows included: the NT emulation grew
// truncate, utimensat, fchdir and linkat in the same wave that closed
// the macOS gap.
func checkFsMeta() {
	s := &softStep{name: "fsmeta"}

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

	// linkat. A hard link is one inode under two names. SameFile compares
	// the identity (inode, or the NT file index), which a copy of the same
	// size cannot fake.
	link := filepath.Join(dir, "hard")
	if s.do("Link", os.Link(path, link)) {
		a, errA := os.Stat(path)
		b, errB := os.Stat(link)
		switch {
		case errA != nil:
			s.do("Link stat", errA)
		case errB != nil:
			s.do("Link stat", errB)
		case !os.SameFile(a, b):
			s.do("Link identity", fmt.Errorf("%s is not the same file as %s", link, path))
		}
	}

	// fchdir. The working directory is process-wide and checkWd asserts
	// against it, so the original is restored before returning. Stat of
	// "." proves the directory changed; a stub that returns 0 does not
	// pass. Identity, not the path string: on NT the temp directory is
	// reached through the /tmp alias and reads back under its real path.
	if wd, err := os.Getwd(); err == nil {
		if d, err := os.Open(dir); err == nil {
			if s.do("Fchdir", syscall.Fchdir(int(d.Fd()))) {
				here, err := os.Stat(".")
				want, werr := os.Stat(dir)
				switch {
				case err != nil:
					s.do("Fchdir stat", err)
				case werr != nil:
					s.do("Fchdir stat", werr)
				case !os.SameFile(here, want):
					s.do("Fchdir identity", fmt.Errorf("working directory after Fchdir is not %s", dir))
				}
			}
			d.Close()
			if err := os.Chdir(wd); err != nil {
				fail("fsmeta", "restore wd %q: %v", wd, err)
				return
			}
		}
	}

	s.finish("sync/truncate/chtimes/link/fchdir")
}

// checkFsLinks covers what the NT port serves of symlinks and the
// permission bits, as a hard assertion on every host: a symlink to a
// file and to a directory reads back, lstat and ReadDir name it as a
// link while stat follows it, removing it leaves the target, and a
// file without its owner's write bit reads that way and still deletes.
func checkFsLinks() {
	s := &softStep{name: "fslinks"}

	dir, err := os.MkdirTemp("", "rp-fslinks")
	if err != nil {
		fail("fslinks", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("target"), 0o644); err != nil {
		fail("fslinks", "write: %v", err)
		return
	}
	sub := filepath.Join(dir, "d")
	if err := os.Mkdir(sub, 0o755); err != nil {
		fail("fslinks", "mkdir: %v", err)
		return
	}

	// An absolute link to the file, and a relative link to the directory.
	sym := filepath.Join(dir, "sym")
	if s.do("Symlink", os.Symlink(path, sym)) {
		if got, err := os.Readlink(sym); s.do("Readlink", err) && got != path {
			s.do("Readlink", fmt.Errorf("target %q, want %q", got, path))
		}
		if fi, err := os.Lstat(sym); s.do("Lstat", err) && fi.Mode()&os.ModeSymlink == 0 {
			s.do("Lstat mode", fmt.Errorf("mode %v, want a symlink", fi.Mode()))
		}
		if fi, err := os.Stat(sym); s.do("Stat", err) && fi.Mode()&os.ModeSymlink != 0 {
			s.do("Stat mode", fmt.Errorf("mode %v, want the target's", fi.Mode()))
		}
		if got, err := os.ReadFile(sym); s.do("ReadFile", err) && string(got) != "target" {
			s.do("ReadFile", fmt.Errorf("read %q through the link, want %q", got, "target"))
		}
	}
	dsym := filepath.Join(dir, "dsym")
	if s.do("Symlink dir", os.Symlink("d", dsym)) {
		if got, err := os.Readlink(dsym); s.do("Readlink dir", err) && got != "d" {
			s.do("Readlink dir", fmt.Errorf("target %q, want %q", got, "d"))
		}
		if _, err := os.ReadDir(dsym); err != nil {
			s.do("ReadDir through the link", err)
		}
	}
	if entries, err := os.ReadDir(dir); s.do("ReadDir", err) {
		links := 0
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				links++
			}
		}
		if links != 2 {
			s.do("ReadDir types", fmt.Errorf("%d entries typed as links, want 2", links))
		}
	}
	if s.do("Remove link", os.Remove(sym)) {
		if _, err := os.Stat(path); err != nil {
			s.do("Remove link kept the target", err)
		}
	}
	if s.do("Remove dir link", os.Remove(dsym)) {
		if _, err := os.Stat(sub); err != nil {
			s.do("Remove dir link kept the directory", err)
		}
	}

	// The owner's write bit is the one every host carries. It reads
	// back, and a file without it still deletes: on unix that was always
	// the directory's call, and on NT unlink clears the attribute.
	if s.do("Chmod 0444", os.Chmod(path, 0o444)) {
		if fi, err := os.Stat(path); s.do("Stat 0444", err) && fi.Mode().Perm()&0o200 != 0 {
			s.do("Chmod 0444 mode", fmt.Errorf("mode %v still has the owner's write bit", fi.Mode().Perm()))
		}
		if s.do("Chmod 0644", os.Chmod(path, 0o644)) {
			if fi, err := os.Stat(path); s.do("Stat 0644", err) && fi.Mode().Perm()&0o200 == 0 {
				s.do("Chmod 0644 mode", fmt.Errorf("mode %v lost the owner's write bit", fi.Mode().Perm()))
			}
		}
		s.do("Chmod 0444 again", os.Chmod(path, 0o444))
	}
	if s.do("Chmod dir 0555", os.Chmod(sub, 0o555)) {
		if fi, err := os.Stat(sub); s.do("Stat dir 0555", err) && fi.Mode().Perm()&0o200 != 0 {
			s.do("Chmod dir 0555 mode", fmt.Errorf("mode %v still has the owner's write bit", fi.Mode().Perm()))
		}
		s.do("Chmod dir 0755", os.Chmod(sub, 0o755))
	}
	s.do("Remove read-only", os.Remove(path))

	s.finish("symlink/readlink/lstat/readdir/chmod/remove")
}

// checkFsMetaUnix covers the metadata syscalls NT has no counterpart
// for: the full unix permission bits, ownership, and FIFOs. It reports
// rather than fails on a Windows host.
func checkFsMetaUnix() {
	s := &softStep{name: "fsmetaunix", soft: cosmoHostOS() == "windows"}

	dir, err := os.MkdirTemp("", "rp-fsmetaunix")
	if err != nil {
		fail("fsmetaunix", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "f")
	f, err := os.Create(path)
	if err != nil {
		fail("fsmetaunix", "create: %v", err)
		return
	}
	defer f.Close()

	// fchmod then fchmodat. Permission bits have the same values on
	// every unix host, so the mode read back is compared exactly.
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

	// mknodat. Apple has no directory-relative mknod, so the darwin
	// emulation serves this only for AT_FDCWD - which is what Mkfifo
	// passes.
	fifo := filepath.Join(dir, "fifo")
	if s.do("Mkfifo", syscall.Mkfifo(fifo, 0o600)) {
		if fi, err := os.Lstat(fifo); err == nil && fi.Mode()&os.ModeNamedPipe == 0 {
			s.do("Mkfifo mode", fmt.Errorf("mode %v, want a named pipe", fi.Mode()))
		}
	}

	s.finish("chmod/chown/mkfifo")
}

// checkVolume covers statfs, fstatfs and uname. Every host serves all
// three, so this is a hard assertion everywhere. It is separate from
// checkSysInfo because the rest of that step is still ENOSYS on NT.
func checkVolume() {
	s := &softStep{name: "volume"}
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
		// A volume cannot have more free blocks than it has blocks. This
		// catches a conversion that divided by the wrong unit, which a
		// positive-value check alone lets through.
		if sfs.Blocks != 0 && sfs.Bfree > sfs.Blocks {
			s.do("Statfs free", fmt.Errorf("bfree %d > blocks %d", sfs.Bfree, sfs.Blocks))
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

	s.finish(strings.Join(detail, " "))
}

func checkSysInfo() {
	s := &softStep{name: "sysinfo", soft: cosmoHostOS() == "windows"}
	var detail []string

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

// checkNanosleep exercises the nanosleep syscall directly. Nothing else
// in the probe reaches it: time.Sleep goes through the runtime's timers
// and its own usleep, so syscall.Nanosleep is a path only a caller of
// that function takes.
//
// It is a syscall that CAN return success while doing nothing, and on
// macOS-Intel it did exactly that for a long time - XNU has no nanosleep
// and the emulation returned 0 without sleeping. So the assertion is on
// the clock, not on the error: a check that only looked at err would
// have passed against a stub that never slept.
//
// The floor is deliberately well under the request. Sleeps overshoot,
// never undershoot, and a scheduler hiccup must not turn this into a
// flake; anything that returns early enough to trip 30ms is a stub, not
// a timing artifact.
func checkNanosleep() {
	s := &softStep{name: "nanosleep", soft: cosmoHostOS() == "windows"}

	const request = 50 * time.Millisecond
	const floor = 30 * time.Millisecond

	req := syscall.NsecToTimespec(int64(request))
	var rem syscall.Timespec
	start := time.Now()
	err := syscall.Nanosleep(&req, &rem)
	elapsed := time.Since(start)

	if !s.do("Nanosleep", err) {
		s.finish("")
		return
	}
	if elapsed < floor {
		s.do("Nanosleep duration", fmt.Errorf("returned after %v, want at least %v - the call did not sleep", elapsed, floor))
	}
	// An uninterrupted sleep leaves nothing remaining. A signal during
	// this window is possible in principle, so a nonzero remainder is
	// only wrong if it exceeds what was asked for.
	if left := time.Duration(syscall.TimespecToNsec(rem)); left > request {
		s.do("Nanosleep remainder", fmt.Errorf("rem = %v, longer than the %v requested", left, request))
	}
	s.finish(fmt.Sprintf("slept %v for a %v request", elapsed.Round(time.Millisecond), request))
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
	s := &softStep{name: "sendfile"}

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
