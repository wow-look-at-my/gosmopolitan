// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows NT process support (wave 2 chunk B): pipe2, posix_spawn-style
// CreateProcessW, and wait4.
//
// The unix-shaped os/exec stack reaches this file through two seams:
//
//   - syscall.forkAndExecInChild (src/syscall/exec_cosmo.go) branches
//     to ntForkExec on NT hosts BEFORE any fork machinery, which
//     builds the Windows command line and environment block (string
//     algebra lives in src/syscall/exec_cosmo_nt.go, ported from
//     upstream exec_windows.go) and calls the WindowsFns.Spawn hook =
//     ntSpawn here. There is no fork and no child-side code: the
//     status pipe forkExec allocates is never inherited (CreatePipe
//     handles are born non-inheritable), so the parent's status read
//     sees EOF immediately - the "exec succeeded" path - and
//     CreateProcessW failures surface synchronously as the
//     forkAndExecInChild errno.
//
//   - SYS_PIPE2 and SYS_WAIT4 are ordinary emulated syscalls
//     (os_cosmo_nt_sys.go dispatcher -> backends here).
//     blockUntilWaitable's SYS_WAITID stays ENOSYS on purpose: package
//     os documents exactly that fallback (wait_waitid.go returns
//     (false, nil) on ENOSYS and pidWait proceeds to the blocking
//     Wait4), so the emulation only needs wait4.
//
// THE WAIT-STATUS PROTOCOL (the design decision of this chunk; the
// parent-side wait4 emulation is the single decode point):
//
//	The NT exit code crosses the process boundary RAW (runtime exit()
//	keeps passing plain codes to ExitProcess - native interop: a cosmo
//	child of a native Windows program reports exit(42) as 42). The
//	Linux wait-status packing happens entirely in the PARENT's wait4:
//
//	  - code < 0xC0000000: normal exit -> status = (code&0xff)<<8
//	    (WIFEXITED; Linux truncates exit codes to 8 bits, so do we).
//	  - NTSTATUS-looking codes (>= 0xC0000000) become fake "killed by
//	    signal" statuses with LINUX signal numbers in the low 7 bits,
//	    mirroring the darwin leg's Linux-numbered wait4 convention so
//	    syscall_cosmo.go's linux WaitStatus algebra decodes unchanged:
//	      0xC0000005 ACCESS_VIOLATION     -> 11 SIGSEGV
//	      0xC0000006 IN_PAGE_ERROR        ->  7 SIGBUS
//	      0xC000008D..93 FLT_*            ->  8 SIGFPE
//	      0xC0000094 INT_DIVIDE_BY_ZERO   ->  8 SIGFPE
//	      0xC0000095 INT_OVERFLOW         ->  8 SIGFPE
//	      0xC000001D ILLEGAL_INSTRUCTION  ->  4 SIGILL
//	      0xC000013A CONTROL_C_EXIT       ->  2 SIGINT
//	      0xC0DE0001..0xC0DE007F          -> code&0x7f (see below)
//	      any other >= 0xC0000000         ->  9 SIGKILL
//	  - 0xC0DE0000|signo is the fork-private encoding reserved for the
//	    signals chunk: dieFromSignal/kill emulation exits the victim
//	    with that code so the parent can report death by an arbitrary
//	    Linux signal (SIGUSR1 has no NTSTATUS). It sits in the NTSTATUS
//	    severity-error range so foreign parents still see "crashed".
//
//	GetExitCodeProcess's STILL_ACTIVE=259 ambiguity does not arise:
//	the code is only read after WaitForSingleObject reported the
//	process signaled (a live process is never queried), so 259 from a
//	child that exit(259)'d decodes honestly as (259&0xff)<<8.
//
// Process handles: CreateProcessW's hProcess is stashed in a fixed
// pid->handle table (the fd table is fd-indexed and does not fit);
// wait4 is the reaping point - it closes the handle and frees the
// slot. hThread is closed immediately. Unknown pids (never spawned, or
// already reaped) fail wait4 with ECHILD, and pid<=0 (wait-any /
// process groups) is ECHILD too: NT has no process-group wait, and
// package os always waits on explicit pids.
//
// Spawns are serialized by the syscall layer (ntSpawnMu in
// exec_cosmo_nt.go) because the stdio handles must be temporarily
// inheritable duplicates: acquireForkLock does NOT mutually exclude
// concurrent forkers, and a concurrent CreateProcessW with
// bInheritHandles=TRUE would capture another spawn's in-flight dupes.
// Everything else in the process stays non-inheritable (CreateFileW
// and CreatePipe handles are born so), which is what makes
// bInheritHandles=TRUE safe at all. PROC_THREAD_ATTRIBUTE_HANDLE_LIST
// hardening (explicit inheritance lists) is deliberately left out this
// wave: it needs the STARTUPINFOEXW attribute-list size dance and has
// the NULL-handle poisoning gotcha; the serialization gives the same
// guarantee process-locally.

package runtime

import "unsafe"

// Win32 constants for the exec surface.
const (
	_NT_DUPLICATE_SAME_ACCESS      = 0x2
	_NT_STARTF_USESTDHANDLES       = 0x100
	_NT_CREATE_UNICODE_ENVIRONMENT = 0x400

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
func ntProcCommit(pid uint32, handle uintptr) {
	lock(&ntProcLock)
	ntProcReserved--
	for i := range ntProcTable {
		if ntProcTable[i].pid == 0 {
			ntProcTable[i] = ntProcEntry{pid: pid, handle: handle}
			unlock(&ntProcLock)
			return
		}
	}
	unlock(&ntProcLock)
	throw("ntProcCommit: no free slot after reservation")
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
// CreateProcessW. argv0 and dir are linux-shaped paths (translated
// here with the chunk-A path layer); cmdline and env arrive as
// ready-made UTF-16 blocks from the syscall layer (which owns the
// quoting and sorting algebra). stdio holds the parent fds for the
// child's std handles, -1 meaning "none" (NULL std handle). The
// caller (syscall.ntForkExec) holds ntSpawnMu, serializing the
// inheritable-dupe window - see the file comment.
//
// The cmdline slice is passed to CreateProcessW as the MUTABLE
// lpCommandLine (the API is documented to scribble on it), which is
// why the syscall layer always builds it in a fresh []uint16.
func ntSpawn(argv0, dir string, cmdline, env []uint16, stdio [3]int32) (pid int32, errno uintptr) {
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
	r, werr := ntcallSE10(ntCreateProcessWFn,
		uintptr(unsafe.Pointer(&wapp[0])),    // lpApplicationName
		uintptr(unsafe.Pointer(&cmdline[0])), // lpCommandLine (mutable)
		0,                                    // lpProcessAttributes
		0,                                    // lpThreadAttributes
		1,                                    // bInheritHandles
		_NT_CREATE_UNICODE_ENVIRONMENT,       // dwCreationFlags
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
	ntProcCommit(pi.processId, pi.hProcess)
	return int32(pi.processId), 0
}

// ---- wait4 ----

// ntFiletimeToMicros converts a FILETIME duration (100ns ticks) to
// microseconds.
func ntFiletimeToMicros(ft uint64) int64 {
	return int64(ft / 10)
}

// ntWaitStatusFromExitCode packs an NT exit code into a Linux wait
// status word per the protocol at the top of this file.
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

// ntEmuWait4 implements Linux wait4 for pids spawned by ntSpawn.
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
