// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows NT process support: pipe2, CreateProcessW-based spawn, and
// wait4.
//
// The unix-shaped os/exec stack reaches this file two ways.
// syscall.forkAndExecInChild branches to ntForkExec before any fork
// machinery and calls the WindowsFns.Spawn hook, which is ntSpawn.
// There is no fork and no child-side code: the status pipe forkExec
// allocates is never inherited, so the parent's read sees EOF at once
// (the "exec succeeded" path) and a CreateProcessW failure surfaces
// synchronously. SYS_PIPE2 and SYS_WAIT4 are ordinary emulated
// syscalls. SYS_WAITID stays ENOSYS on purpose: package os documents
// that fallback, so the emulation only needs wait4.

package runtime

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// Win32 constants for the exec surface.
const (
	_NT_STARTF_USESTDHANDLES       = 0x100
	_NT_CREATE_UNICODE_ENVIRONMENT = 0x400
	_NT_CREATE_NEW_PROCESS_GROUP   = 0x200

	_NT_WAIT_OBJECT_0 = 0
	_NT_WAIT_TIMEOUT  = 0x102
	_NT_WAIT_FAILED   = 0xFFFFFFFF

	_NT_WNOHANG = 1 // Linux wait4 option

	// ntWaitSigBase is the fork-private "killed by Linux signal N"
	// exit-code base (see the protocol note above).
	ntWaitSigBase = 0xC0DE0000
)

// ntStartupInfoW is win64 STARTUPINFOW (104 bytes).
type ntStartupInfoW struct {
	cb              uint32
	_               uint32
	lpReserved      uintptr
	lpDesktop       uintptr
	lpTitle         uintptr
	dwX             uint32
	dwY             uint32
	dwXSize         uint32
	dwYSize         uint32
	dwXCountChars   uint32
	dwYCountChars   uint32
	dwFillAttribute uint32
	dwFlags         uint32
	wShowWindow     uint16
	cbReserved2     uint16
	_               uint32
	lpReserved2     uintptr
	hStdInput       uintptr
	hStdOutput      uintptr
	hStdError       uintptr
}

// ntProcessInformation is PROCESS_INFORMATION (24 bytes).
type ntProcessInformation struct {
	hProcess  uintptr
	hThread   uintptr
	processId uint32
	threadId  uint32
}

// ntLinuxRusage matches syscall.Rusage (Linux amd64 struct rusage,
// 144 bytes): two timevals then 14 int64 counters. Only utime/stime
// are fillable from GetProcessTimes; the counters stay zero
// (unknowable on NT), same as the darwin leg's partial rusage.
type ntLinuxRusage struct {
	utime ntLinuxTimeval
	stime ntLinuxTimeval
	other [14]int64
}

type ntLinuxTimeval struct {
	sec  int64
	usec int64
}

// ---- pid -> process handle table ----

const ntProcMax = 64

type ntProcEntry struct {
	pid    uint32
	handle uintptr
	// pgleader records that the child was spawned with
	// CREATE_NEW_PROCESS_GROUP (SysProcAttr{Setpgid: true}): its pid
	// doubles as its process-group id, making the group addressable
	// by the emulated kill(-pgid) (wave 3 item 4).
	pgleader bool
}

var (
	ntProcLock     mutex
	ntProcTable    [ntProcMax]ntProcEntry // pid == 0 means free
	ntProcReserved int32
)

// ntProcReserve claims capacity for one child before CreateProcessW
// runs, so a full table fails the spawn cleanly (EAGAIN) instead of
// leaving a running child unwaitable.
func ntProcReserve() bool {
	lock(&ntProcLock)
	used := ntProcReserved
	for i := range ntProcTable {
		if ntProcTable[i].pid != 0 {
			used++
		}
	}
	if used >= ntProcMax {
		unlock(&ntProcLock)
		return false
	}
	ntProcReserved++
	unlock(&ntProcLock)
	return true
}

func ntProcUnreserve() {
	lock(&ntProcLock)
	ntProcReserved--
	unlock(&ntProcLock)
}

// ntProcCommit turns the reservation into a real entry. Guaranteed a
// free slot by ntProcReserve.
func ntProcCommit(pid uint32, handle uintptr, pgleader bool) {
	lock(&ntProcLock)
	ntProcReserved--
	for i := range ntProcTable {
		if ntProcTable[i].pid == 0 {
			ntProcTable[i] = ntProcEntry{pid: pid, handle: handle, pgleader: pgleader}
			unlock(&ntProcLock)
			return
		}
	}
	unlock(&ntProcLock)
	throw("ntProcCommit: no free slot after reservation")
}

// ntProcFindGroup returns the process handle of the spawned child
// whose pid IS the given process-group id - i.e. a child launched as
// its own group leader (CREATE_NEW_PROCESS_GROUP). Children that are
// not group leaders, and pids we never spawned, are not addressable
// as groups (the caller reports ESRCH), mirroring the
// own-children-only rule of ntEmuKill's positive-pid arm.
func ntProcFindGroup(pgid uint32) (uintptr, bool) {
	lock(&ntProcLock)
	for i := range ntProcTable {
		if ntProcTable[i].pid == pgid && ntProcTable[i].pgleader {
			h := ntProcTable[i].handle
			unlock(&ntProcLock)
			return h, true
		}
	}
	unlock(&ntProcLock)
	return 0, false
}

// ntProcFind returns the handle for pid without removing it (wait4
// with WNOHANG must be able to poll repeatedly).
func ntProcFind(pid uint32) (uintptr, bool) {
	lock(&ntProcLock)
	for i := range ntProcTable {
		if ntProcTable[i].pid == pid {
			h := ntProcTable[i].handle
			unlock(&ntProcLock)
			return h, true
		}
	}
	unlock(&ntProcLock)
	return 0, false
}

// ntProcRemove frees the entry; reports whether this caller won the
// removal (a concurrent wait4 on the same pid loses and returns
// ECHILD, matching a double reap on Linux).
func ntProcRemove(pid uint32) bool {
	lock(&ntProcLock)
	for i := range ntProcTable {
		if ntProcTable[i].pid == pid {
			ntProcTable[i] = ntProcEntry{}
			unlock(&ntProcLock)
			return true
		}
	}
	unlock(&ntProcLock)
	return false
}

// ---- pipe2 ----

// ntEmuPipe2 implements Linux pipe2 over CreatePipe. The NULL
// SECURITY_ATTRIBUTES makes both handles non-inheritable, which is the
// correct O_CLOEXEC-shaped default here: NT children inherit only the
// explicitly duplicated stdio handles (ntSpawn), never arbitrary fds,
// so cloexec-ness is effectively always on and the O_CLOEXEC flag is
// only recorded for fcntl round-trips. O_NONBLOCK is accepted and
// recorded but reads/writes stay blocking (anonymous pipes have no
// nonblocking mode without PeekNamedPipe emulation; nothing in the
// standard library needs it - internal/poll only sets nonblocking on
// fds it could register with the netpoller, and netpollopen refuses
// pipe fds on NT so they run in blocking mode).
func ntEmuPipe2(p *[2]int32, flags int32) (r1, r2, errno uintptr) {
	if p == nil {
		return ntFail3(ntEINVAL)
	}
	if flags&^int32(_NT_O_CLOEXEC|_NT_O_NONBLOCK) != 0 {
		return ntFail3(ntEINVAL)
	}
	var rh, wh uintptr
	r, werr := ntcallE(ntCreatePipeFn, uintptr(unsafe.Pointer(&rh)),
		uintptr(unsafe.Pointer(&wh)), 0, 0, 0, 0, 0)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	cloexec := flags&_NT_O_CLOEXEC != 0
	status := flags & _NT_O_NONBLOCK
	rfd := ntFDAlloc(rh, ntFDPipe, _NT_O_RDONLY|status, cloexec, nil)
	if rfd < 0 {
		ntcall(ntCloseHandleFn, rh, 0, 0, 0, 0, 0)
		ntcall(ntCloseHandleFn, wh, 0, 0, 0, 0, 0)
		return ntFail3(uintptr(-rfd))
	}
	wfd := ntFDAlloc(wh, ntFDPipe, _NT_O_WRONLY|status, cloexec, nil)
	if wfd < 0 {
		if h, _, ok := ntFDRelease(rfd); ok {
			ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
		}
		ntcall(ntCloseHandleFn, wh, 0, 0, 0, 0, 0)
		return ntFail3(uintptr(-wfd))
	}
	ntFDSetFtype(rfd, _NT_FILE_TYPE_PIPE)
	ntFDSetFtype(wfd, _NT_FILE_TYPE_PIPE)
	p[0], p[1] = rfd, wfd
	return 0, 0, 0
}

// ---- spawn ----

// ntSpawn is the WindowsFns.Spawn hook: launch a child with
// CreateProcessW. argv0 and dir are linux-shaped paths. cmdline and
// env are ready-made UTF-16 blocks, and stdio holds the parent fds for
// the child's std handles, -1 for a NULL one. SpawnNewProcessGroup
// records the child as its own group leader for kill(-pgid).
//
// The caller (syscall.ntForkExec) holds ntSpawnMu. Spawns MUST stay
// serialized: the stdio handles are temporarily inheritable dupes,
// acquireForkLock does not exclude concurrent forkers, and a
// concurrent bInheritHandles=TRUE would capture another spawn's dupes.
// The cmdline slice reaches CreateProcessW as the MUTABLE
// lpCommandLine, so the syscall layer builds it in a fresh []uint16.
func ntSpawn(argv0, dir string, cmdline, env []uint16, stdio [3]int32, flags uint32) (pid int32, errno uintptr) {
	wapp := ntPathW(argv0)
	if wapp == nil {
		return 0, ntENOENT
	}
	var wdir []uint16
	if dir != "" {
		if wdir = ntPathW(dir); wdir == nil {
			return 0, ntENOENT
		}
	}
	if len(cmdline) == 0 || cmdline[len(cmdline)-1] != 0 {
		return 0, ntEINVAL
	}

	if !ntProcReserve() {
		return 0, ntEAGAIN
	}

	// Inheritable duplicates of the requested stdio handles. Cleaned
	// up on every path; while they exist, no other spawn may run
	// (ntSpawnMu) or it would inherit them too.
	var dups [3]uintptr
	closeDups := func() {
		for _, d := range dups {
			if d != 0 {
				ntcall(ntCloseHandleFn, d, 0, 0, 0, 0, 0)
			}
		}
	}
	for i, fd := range stdio {
		if fd < 0 {
			continue
		}
		e, ok := ntFDLookup(fd)
		if !ok || e.handle == 0 || e.handle == _NT_INVALID_HANDLE_VALUE {
			closeDups()
			ntProcUnreserve()
			return 0, ntEBADF
		}
		var dup uintptr
		r, _ := ntcallE(ntDuplicateHandleFn,
			_NT_CURRENT_PROCESS, e.handle, _NT_CURRENT_PROCESS,
			uintptr(unsafe.Pointer(&dup)),
			0, // dwDesiredAccess (ignored with SAME_ACCESS)
			1, // bInheritHandle = TRUE
			_NT_DUPLICATE_SAME_ACCESS)
		if r == 0 {
			closeDups()
			ntProcUnreserve()
			return 0, ntEBADF
		}
		dups[i] = dup
	}

	var si ntStartupInfoW
	si.cb = uint32(unsafe.Sizeof(si))
	si.dwFlags = _NT_STARTF_USESTDHANDLES
	si.hStdInput = dups[0]
	si.hStdOutput = dups[1]
	si.hStdError = dups[2]
	var pi ntProcessInformation

	var envp, dirp uintptr
	if len(env) > 0 {
		envp = uintptr(unsafe.Pointer(&env[0]))
	}
	if wdir != nil {
		dirp = uintptr(unsafe.Pointer(&wdir[0]))
	}
	creation := uintptr(_NT_CREATE_UNICODE_ENVIRONMENT)
	pgleader := flags&cosmo.SpawnNewProcessGroup != 0
	if pgleader {
		// The child becomes the leader of a new process group whose
		// id is its pid; note NT then also DISABLES Ctrl-C in the
		// child until it opts back in (the documented
		// CREATE_NEW_PROCESS_GROUP side effect). CTRL_BREAK delivery
		// is unaffected.
		creation |= _NT_CREATE_NEW_PROCESS_GROUP
	}
	r, werr := ntcallSE10(ntCreateProcessWFn,
		uintptr(unsafe.Pointer(&wapp[0])),    // lpApplicationName
		uintptr(unsafe.Pointer(&cmdline[0])), // lpCommandLine (mutable)
		0,                                    // lpProcessAttributes
		0,                                    // lpThreadAttributes
		1,                                    // bInheritHandles
		creation,                             // dwCreationFlags
		envp,                                 // lpEnvironment (UTF-16 block)
		dirp,                                 // lpCurrentDirectory
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)))
	closeDups()
	KeepAlive(wapp)
	KeepAlive(cmdline)
	KeepAlive(env)
	KeepAlive(wdir)
	if r == 0 {
		ntProcUnreserve()
		return 0, ntErrno(werr)
	}
	ntcall(ntCloseHandleFn, pi.hThread, 0, 0, 0, 0, 0)
	ntProcCommit(pi.processId, pi.hProcess, pgleader)
	return int32(pi.processId), 0
}

// ---- wait4 ----

// ntFiletimeToMicros converts a FILETIME duration (100ns ticks) to
// microseconds.
func ntFiletimeToMicros(ft uint64) int64 {
	return int64(ft / 10)
}

// ntWaitStatusFromExitCode packs an NT exit code into a Linux wait
// status word. The exit code crosses the process boundary RAW - a
// cosmo child of a native Windows program reports exit(42) as 42 - so
// this parent-side call is the single decode point. A code below
// 0xC0000000 is a normal exit, truncated to 8 bits as Linux does.
// Above it, the status becomes "killed by signal" with a LINUX signal
// number, which is the darwin leg's convention too, so syscall's linux
// WaitStatus algebra decodes it unchanged. 0xC0DE0000|signo is the
// fork-private encoding the kill emulation exits a victim with, for a
// signal NTSTATUS has no name for; it sits in the severity-error range
// so a foreign parent still sees a crash. STILL_ACTIVE=259 is never
// ambiguous here: the code is read only after the process signaled.
func ntWaitStatusFromExitCode(code uint32) int32 {
	sig := uint32(0)
	switch {
	case code == 0xC0000005: // ACCESS_VIOLATION
		sig = 11 // SIGSEGV
	case code == 0xC0000006: // IN_PAGE_ERROR
		sig = 7 // SIGBUS
	case code >= 0xC000008D && code <= 0xC0000095:
		// FLT_DENORMAL..FLT_UNDERFLOW, INT_DIVIDE_BY_ZERO, INT_OVERFLOW.
		sig = 8 // SIGFPE
	case code == 0xC000001D: // ILLEGAL_INSTRUCTION
		sig = 4 // SIGILL
	case code == 0xC000013A: // STATUS_CONTROL_C_EXIT
		sig = 2 // SIGINT
	case code&0xFFFFFF80 == ntWaitSigBase && code&0x7F != 0:
		sig = code & 0x7F // fork-private "killed by Linux signal N"
	case code >= 0xC0000000:
		sig = 9 // unknown NTSTATUS crash -> SIGKILL
	}
	if sig != 0 {
		return int32(sig) // WIFSIGNALED
	}
	return int32(code&0xff) << 8 // WIFEXITED
}

// ntEmuWait4 implements Linux wait4 for pids spawned by ntSpawn. It is
// the reaping point: CreateProcessW's hProcess lives in a fixed
// pid->handle table (the fd table is fd-indexed and does not fit), and
// this closes the handle and frees the slot. A pid never spawned, or
// already reaped, is ECHILD.
func ntEmuWait4(pid int32, wstatus *int32, options int32, rusage *ntLinuxRusage) (r1, r2, errno uintptr) {
	if pid <= 0 {
		// Wait-any and process-group waits are unsupported: NT has no
		// process groups, and package os always names the pid.
		return ntFail3(ntECHILD)
	}
	h, ok := ntProcFind(uint32(pid))
	if !ok {
		return ntFail3(ntECHILD)
	}
	timeout := uintptr(_NT_INFINITE)
	if options&_NT_WNOHANG != 0 {
		timeout = 0
	}
	// Blocking kernel wait: entersyscall-bracketed so the P is
	// released while this M parks in WaitForSingleObject.
	w, werr := ntcallSE(ntWaitForSingleObjectFn, h, timeout, 0, 0, 0, 0, 0)
	switch uint32(w) {
	case _NT_WAIT_OBJECT_0:
		// Child exited; fall through to reap.
	case _NT_WAIT_TIMEOUT:
		return 0, 0, 0 // WNOHANG: nothing to reap yet
	default:
		return ntFail3(ntErrno(werr))
	}

	var code uint32
	if r, werr := ntcallE(ntGetExitCodeProcessFn, h, uintptr(unsafe.Pointer(&code)), 0, 0, 0, 0, 0); r == 0 {
		return ntFail3(ntErrno(werr))
	}
	if rusage != nil {
		*rusage = ntLinuxRusage{}
		var creation, exit, kernel, user uint64
		if r, _ := ntcallE(ntGetProcessTimesFn, h,
			uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
			uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)), 0, 0); r != 0 {
			ku := ntFiletimeToMicros(kernel)
			uu := ntFiletimeToMicros(user)
			rusage.stime = ntLinuxTimeval{sec: ku / 1e6, usec: ku % 1e6}
			rusage.utime = ntLinuxTimeval{sec: uu / 1e6, usec: uu % 1e6}
		}
	}
	if !ntProcRemove(uint32(pid)) {
		// A concurrent wait4 reaped it first (and closed the handle).
		return ntFail3(ntECHILD)
	}
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
	if wstatus != nil {
		*wstatus = ntWaitStatusFromExitCode(code)
	}
	return uintptr(pid), 0, 0
}
