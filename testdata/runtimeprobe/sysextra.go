package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Syscalls that were ENOSYS on macOS until the emulation grew them, and
// that nothing else in the probe reaches. Each is a call a caller makes
// directly: the standard library never routes through any of them.

// checkDurable covers fdatasync and sync. os.File.Sync is fsync, a
// different syscall, so fdatasync has no in-tree caller to break it.
//
// A hard assertion on every host, Windows included: the NT emulation
// serves fdatasync through FlushFileBuffers, and sync reports nothing
// anywhere.
func checkDurable() {
	s := &softStep{name: "durable"}

	dir, err := os.MkdirTemp("", "rp-durable")
	if err != nil {
		fail("durable", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	f, err := os.Create(filepath.Join(dir, "f"))
	if err != nil {
		fail("durable", "create: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write([]byte("durable")); err != nil {
		fail("durable", "write: %v", err)
		return
	}

	s.do("Fdatasync", syscall.Fdatasync(int(f.Fd())))
	// sync(2) returns nothing on either system, so there is no error to
	// assert on. Reaching the call at all is the check: an ENOSYS
	// emulation cannot report anything through this signature, which is
	// exactly why the syscall is easy to leave unimplemented.
	syscall.Sync()

	s.finish("fdatasync/sync")
}

// checkRusage covers getrusage and gettimeofday. Both carry a struct
// timeval, whose microsecond field is 32 bits on Apple and 64 on Linux,
// so a forwarded buffer would keep its size and lose its contents. The
// assertions are therefore on the VALUES, not on the error: garbage in
// the wide half of a microsecond field is what a missing conversion
// looks like.
func checkRusage() {
	s := &softStep{name: "rusage", soft: cosmoHostOS() == "windows"}
	var detail []string

	var ru syscall.Rusage
	if s.do("Getrusage", syscall.Getrusage(syscall.RUSAGE_SELF, &ru)) {
		// A process that has reached this line has burned some CPU and
		// touched some memory, and no field may hold a nonsense value.
		cpu := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
		cpu += time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
		switch {
		case ru.Utime.Usec < 0 || ru.Utime.Usec > 999999:
			s.do("Getrusage utime", fmt.Errorf("usec %d outside 0..999999 - the timeval was not converted", ru.Utime.Usec))
		case ru.Stime.Usec < 0 || ru.Stime.Usec > 999999:
			s.do("Getrusage stime", fmt.Errorf("usec %d outside 0..999999 - the timeval was not converted", ru.Stime.Usec))
		case cpu <= 0:
			s.do("Getrusage cpu", fmt.Errorf("%v of CPU used, want more than zero", cpu))
		case ru.Maxrss <= 0:
			s.do("Getrusage maxrss", fmt.Errorf("%d, want a positive resident-set size", ru.Maxrss))
		default:
			detail = append(detail, fmt.Sprintf("cpu=%v", cpu.Round(time.Millisecond)))
		}
	}

	var tv syscall.Timeval
	if s.do("Gettimeofday", syscall.Gettimeofday(&tv)) {
		// Held against the runtime's own clock rather than a constant:
		// an unconverted Apple timeval reads a plausible second count
		// and a microsecond field with the padding in its top half.
		got := time.Unix(tv.Sec, tv.Usec*1000)
		if skew := time.Since(got); skew < -time.Minute || skew > time.Minute {
			s.do("Gettimeofday value", fmt.Errorf("reports %v, %v away from time.Now()", got, skew))
		} else if tv.Usec < 0 || tv.Usec > 999999 {
			s.do("Gettimeofday usec", fmt.Errorf("usec %d outside 0..999999", tv.Usec))
		} else {
			detail = append(detail, "clock=agrees")
		}
	}

	s.finish(strings.Join(detail, " "))
}

// linuxTIOCGWINSZ is the Linux ioctl request for the terminal window
// size. A cosmo binary speaks the Linux ABI, so this is the number a
// caller passes on every host; the macOS emulation translates it to
// Apple's. The syscall package carries no name for it (its zerrors
// covers the job-control requests only), hence the literal.
const linuxTIOCGWINSZ = 0x5413

type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// checkIoctl covers the ioctl emulation. CI runs the probe with no
// controlling terminal, so the assertion is on WHICH failure comes back:
// ENOTTY means the request reached the kernel and the descriptor simply
// is not a terminal, while ENOSYS means the emulation never served the
// syscall at all. That distinction is the whole check, and it holds
// whether or not a terminal is attached.
func checkIoctl() {
	s := &softStep{name: "ioctl", soft: cosmoHostOS() == "windows"}

	var ws winsize
	err := syscall.Ioctl(int(os.Stdin.Fd()), linuxTIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	switch {
	case err == syscall.ENOSYS:
		s.do("Ioctl TIOCGWINSZ", fmt.Errorf("ENOSYS: this host does not serve the request"))
		s.finish("")
	case err == nil && ws.Row == 0 && ws.Col == 0:
		s.do("Ioctl TIOCGWINSZ", fmt.Errorf("succeeded and reported a 0x0 terminal"))
		s.finish("")
	case err == nil:
		s.finish(fmt.Sprintf("%dx%d terminal", ws.Col, ws.Row))
	case err == syscall.ENOTTY:
		s.finish("ENOTTY with no terminal attached, which is the kernel answering")
	default:
		// Another errno is still the kernel answering rather than the
		// emulation refusing, so it passes - with the value named, since
		// this is the branch nobody predicted.
		s.finish("stdin answered " + err.Error())
	}
}
