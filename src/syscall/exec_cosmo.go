// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// SysProcIDMap holds Container ID to Host ID mappings used for User Namespaces.
type SysProcIDMap struct {
	ContainerID int // Container ID.
	HostID      int // Host ID.
	Size        int // Size.
}

type SysProcAttr struct {
	Chroot     string      // Chroot.
	Credential *Credential // Credential.
	// Ptrace tells the child to call ptrace(PTRACE_TRACEME).
	// Call runtime.LockOSThread before starting a process with this set,
	// and don't call UnlockOSThread until done with PtraceSyscall calls.
	Ptrace bool
	Setsid bool // Create session.
	// Setpgid sets the process group ID of the child to Pgid,
	// or, if Pgid == 0, to the new child's process ID.
	Setpgid bool
	// Setctty sets the controlling terminal of the child to
	// file descriptor Ctty. Ctty must be a descriptor number
	// in the child process: an index into ProcAttr.Files.
	// This is only meaningful if Setsid is true.
	Setctty bool
	Noctty  bool // Detach fd 0 from controlling terminal.
	Ctty    int  // Controlling TTY fd.
	// Foreground places the child process group in the foreground.
	// This implies Setpgid. The Ctty field must be set to
	// the descriptor of the controlling TTY.
	// Unlike Setctty, in this case Ctty must be a descriptor
	// number in the parent process.
	Foreground bool
	Pgid       int // Child's process group ID if Setpgid.
	// Pdeathsig, if non-zero, is a signal that the kernel will send to
	// the child process when the creating thread dies.
	Pdeathsig    Signal
	Cloneflags   uintptr        // Flags for clone calls (not used on cosmo).
	Unshareflags uintptr        // Flags for unshare calls (not used on cosmo).
	UidMappings  []SysProcIDMap // User ID mappings for user namespaces.
	GidMappings  []SysProcIDMap // Group ID mappings for user namespaces.
	// GidMappingsEnableSetgroups enabling setgroups syscall.
	GidMappingsEnableSetgroups bool
	AmbientCaps                []uintptr // Ambient capabilities (not used on cosmo).
	UseCgroupFD                bool      // Whether to make use of the CgroupFD field.
	CgroupFD                   int       // File descriptor of a cgroup to put the new process into.
	PidFD                      *int      // Store pidfd of child if supported.
}

var (
	none  = [...]byte{'n', 'o', 'n', 'e', 0}
	slash = [...]byte{'/', 0}
)

// Implemented in runtime package.
func runtime_BeforeFork()
func runtime_AfterFork()
func runtime_AfterForkInChild()

// Fork, dup fd onto 0..len(fd), and exec(argv0, argvv, envv) in child.
// If a dup or exec fails, write the errno error to pipe.
// (Pipe is close-on-exec so if exec succeeds, it will be closed.)
// In the child, this function must not acquire any locks, because
// they might have been locked at the time of the fork. This means
// no rescheduling, no malloc calls, and no new stack segments.
//
//go:norace
func forkAndExecInChild(argv0 *byte, argv, envv []*byte, chroot, dir *byte, attr *ProcAttr, sys *SysProcAttr, pipe int) (pid int, err Errno) {
	// NT host: no fork exists. Launch the child posix_spawn-style via
	// CreateProcessW instead (exec_cosmo_nt.go) - BEFORE any fork
	// machinery. The status pipe is not passed down: the child cannot
	// inherit it, so the parent's status read sees EOF (the success
	// protocol) and spawn failures return synchronously from here.
	if cosmo.Windows() != nil {
		return ntForkExec(argv0, argv, envv, chroot, dir, attr, sys)
	}

	// Declare all variables at top in case any
	// temporary variables get allocated during
	// the actual syscall sequences.
	var (
		r1     uintptr
		err1   Errno
		nextfd int
		i      int
	)

	// Guard against side effects of shuffling fds below.
	// Make sure that nextfd is beyond any currently open files so
	// that we can't run the risk of overwriting any of them.
	// A nil entry in ProcAttr.Files becomes ^uintptr(0), which is -1
	// as an int and means "leave that fd closed in the child".
	fd := make([]int, len(attr.Files))
	nextfd = len(attr.Files)
	for i, ufd := range attr.Files {
		if nextfd < int(ufd) {
			nextfd = int(ufd)
		}
		fd[i] = int(ufd)
	}
	nextfd++

	// An APE cannot be exec'd directly, so prepare its /bin/sh form now,
	// while allocation is still legal. The child retries with it when
	// execve answers ENOEXEC. See exec_cosmo_ape.go.
	var shArgv []*byte
	if isAPEPath(dir, argv0) {
		shArgv = apeShellArgv(argv0, argv)
	}

	// About to call fork.
	// No more allocation or calls of non-assembly functions.
	runtime_BeforeFork()
	// Use clone with SIGCHLD to emulate fork. This works on both amd64 (which
	// has SYS_FORK) and arm64 (which only has SYS_CLONE). Using clone ensures
	// portability across architectures.
	r1, err1 = rawVforkSyscall(SYS_CLONE, uintptr(SIGCHLD), 0, 0)
	if err1 != 0 {
		runtime_AfterFork()
		return 0, err1
	}

	if r1 != 0 {
		// parent; return PID
		runtime_AfterFork()
		return int(r1), 0
	}

	// Fork succeeded, now in child.
	runtime_AfterForkInChild()

	// Session ID
	if sys.Setsid {
		_, _, err1 = RawSyscall(SYS_SETSID, 0, 0, 0)
		if err1 != 0 {
			goto childerror
		}
	}

	// Set process group
	if sys.Setpgid || sys.Foreground {
		// Place child in process group.
		_, _, err1 = RawSyscall(SYS_SETPGID, 0, uintptr(sys.Pgid), 0)
		if err1 != 0 {
			goto childerror
		}
	}

	// Chroot
	if chroot != nil {
		_, _, err1 = RawSyscall(SYS_CHROOT, uintptr(unsafe.Pointer(chroot)), 0, 0)
		if err1 != 0 {
			goto childerror
		}
	}

	// User and groups
	if cred := sys.Credential; cred != nil {
		ngroups := uintptr(len(cred.Groups))
		groups := uintptr(0)
		if ngroups > 0 {
			groups = uintptr(unsafe.Pointer(&cred.Groups[0]))
		}
		if !cred.NoSetGroups {
			_, _, err1 = RawSyscall(SYS_SETGROUPS, ngroups, groups, 0)
			if err1 != 0 {
				goto childerror
			}
		}
		_, _, err1 = RawSyscall(SYS_SETGID, uintptr(cred.Gid), 0, 0)
		if err1 != 0 {
			goto childerror
		}
		_, _, err1 = RawSyscall(SYS_SETUID, uintptr(cred.Uid), 0, 0)
		if err1 != 0 {
			goto childerror
		}
	}

	// Chdir
	if dir != nil {
		_, _, err1 = RawSyscall(SYS_CHDIR, uintptr(unsafe.Pointer(dir)), 0, 0)
		if err1 != 0 {
			goto childerror
		}
	}

	// Pass 1: look for fd[i] < i and move those up above len(fd)
	// so that pass 2 won't stomp on an fd it needs later.
	if pipe < nextfd {
		_, _, err1 = RawSyscall(SYS_DUP3, uintptr(pipe), uintptr(nextfd), O_CLOEXEC)
		if err1 != 0 {
			goto childerror
		}
		pipe = nextfd
		nextfd++
	}
	for i = 0; i < len(fd); i++ {
		if fd[i] >= 0 && fd[i] < i {
			if nextfd == pipe { // don't stomp on pipe
				nextfd++
			}
			_, _, err1 = RawSyscall(SYS_DUP3, uintptr(fd[i]), uintptr(nextfd), O_CLOEXEC)
			if err1 != 0 {
				goto childerror
			}
			fd[i] = nextfd
			nextfd++
		}
	}

	// Pass 2: dup fd[i] down onto i.
	for i = 0; i < len(fd); i++ {
		if fd[i] == -1 {
			RawSyscall(SYS_CLOSE, uintptr(i), 0, 0)
			continue
		}
		if fd[i] == i {
			// dup2(i, i) won't clear close-on-exec flag on Linux,
			// probably not elsewhere either.
			_, _, err1 = RawSyscall(SYS_FCNTL, uintptr(fd[i]), F_SETFD, 0)
			if err1 != 0 {
				goto childerror
			}
			continue
		}
		// The new fd is created NOT close-on-exec,
		// which is exactly what we want.
		_, _, err1 = RawSyscall(SYS_DUP3, uintptr(fd[i]), uintptr(i), 0)
		if err1 != 0 {
			goto childerror
		}
	}

	// By convention, we don't close-on-exec the fds we are
	// started with, so if len(fd) < 3, close 0, 1, 2 as needed.
	// Programs that know they inherit fds >= 3 will need
	// to set them close-on-exec.
	for i = len(fd); i < 3; i++ {
		RawSyscall(SYS_CLOSE, uintptr(i), 0, 0)
	}

	// Detach fd 0 from tty
	if sys.Noctty {
		_, _, err1 = RawSyscall(SYS_IOCTL, 0, uintptr(TIOCNOTTY), 0)
		if err1 != 0 {
			goto childerror
		}
	}

	// Set the controlling TTY to Ctty
	if sys.Setctty {
		_, _, err1 = RawSyscall(SYS_IOCTL, uintptr(sys.Ctty), uintptr(TIOCSCTTY), 1)
		if err1 != 0 {
			goto childerror
		}
	}

	// Time to exec.
	_, _, err1 = RawSyscall(SYS_EXECVE,
		uintptr(unsafe.Pointer(argv0)),
		uintptr(unsafe.Pointer(&argv[0])),
		uintptr(unsafe.Pointer(&envv[0])))
	if err1 == ENOEXEC && shArgv != nil {
		// The target is an APE and this host has no binfmt_misc entry
		// for it, so hand it to the shell that reads its header.
		_, _, err1 = RawSyscall(SYS_EXECVE,
			uintptr(unsafe.Pointer(&apeShellPath[0])),
			uintptr(unsafe.Pointer(&shArgv[0])),
			uintptr(unsafe.Pointer(&envv[0])))
	}

childerror:
	// send error code on pipe
	RawSyscall(SYS_WRITE, uintptr(pipe), uintptr(unsafe.Pointer(&err1)), unsafe.Sizeof(err1))
	for {
		RawSyscall(SYS_EXIT, 253, 0, 0)
	}
}

// forkAndExecFailureCleanup cleans up after an exec failure.
func forkAndExecFailureCleanup(attr *ProcAttr, sys *SysProcAttr) {
	if sys.PidFD != nil && *sys.PidFD != -1 {
		Close(*sys.PidFD)
		*sys.PidFD = -1
	}
}
