package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkFlock exercises flock(2), which was ENOSYS on every non-Linux host
// until the darwin emulation and the NT LockFileEx mapping grew it.
// Nothing else in the probe reaches it, and nothing in the standard
// library does either: a program that wants a lock file calls
// syscall.Flock itself.
//
// The property under test is exclusion, not the return code. A stub that
// answers 0 and locks nothing passes an error-only check, so every step
// here asserts what the OTHER descriptor can do afterwards.
//
// Two descriptors in one process are enough. flock locks the open file
// description rather than the process, so a second open of the same path
// conflicts with the first even from here - and LockFileEx conflicts
// across two handles the same way.
func checkFlock() {
	s := &softStep{name: "flock"}

	dir, err := os.MkdirTemp("", "rp-flock")
	if err != nil {
		fail("flock", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "lock")
	a, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fail("flock", "open first: %v", err)
		return
	}
	defer a.Close()
	b, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fail("flock", "open second: %v", err)
		return
	}
	defer b.Close()

	if !s.do("Flock LOCK_EX", syscall.Flock(int(a.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)) {
		s.finish("")
		return
	}

	// The whole point of the syscall: the second descriptor is kept out,
	// and it is kept out with EWOULDBLOCK. A caller reads that errno as
	// "somebody else holds it" and anything else as a real failure, so the
	// errno is asserted rather than just the refusal.
	err = syscall.Flock(int(b.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		s.do("exclusion", fmt.Errorf("a second descriptor took the lock while the first held it"))
	case err != syscall.EWOULDBLOCK:
		s.do("exclusion errno", fmt.Errorf("second LOCK_EX|LOCK_NB gave %v, want EWOULDBLOCK", err))
	}

	// Releasing hands the lock over. This is what proves the unlock did
	// something: an unlock that answered 0 and kept the lock leaves the
	// second descriptor still refused.
	if s.do("Flock LOCK_UN", syscall.Flock(int(a.Fd()), syscall.LOCK_UN)) {
		if s.do("relock after unlock", syscall.Flock(int(b.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)) {
			s.do("Flock LOCK_UN second", syscall.Flock(int(b.Fd()), syscall.LOCK_UN))
		}
	}

	// A shared lock lets a second shared lock in and still refuses an
	// exclusive one. LOCK_SH reaches a different code path on NT (no
	// LOCKFILE_EXCLUSIVE_LOCK), so an emulation that ignored the operation
	// bits entirely would only show up here.
	if s.do("Flock LOCK_SH", syscall.Flock(int(a.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)) {
		if s.do("second LOCK_SH", syscall.Flock(int(b.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)) {
			s.do("Flock LOCK_UN shared", syscall.Flock(int(b.Fd()), syscall.LOCK_UN))
		}
		if err := syscall.Flock(int(b.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			s.do("shared vs exclusive", fmt.Errorf("LOCK_EX succeeded against a held LOCK_SH"))
			syscall.Flock(int(b.Fd()), syscall.LOCK_UN)
		}
		s.do("Flock LOCK_UN after shared", syscall.Flock(int(a.Fd()), syscall.LOCK_UN))
	}

	s.finish("exclusive/shared/unlock, EWOULDBLOCK on conflict")
}
