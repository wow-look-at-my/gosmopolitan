// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// Windows support for Cosmopolitan APE binaries.
// When running on Windows, the PE stub sets iswindows = 1
// and the runtime uses Windows API calls instead of Linux syscalls.

// iswindows is set to 1 by the PE stub when running on Windows.
// This is declared in sys_cosmo_windows_amd64.s
var iswindows uint32

// windowstlsslot holds the TLS slot for the G pointer on Windows.
var windowstlsslot uint32

// Windows standard handles
const (
	_STD_INPUT_HANDLE  = -10
	_STD_OUTPUT_HANDLE = -11
	_STD_ERROR_HANDLE  = -12
)

// Windows memory constants
const (
	_MEM_COMMIT     = 0x1000
	_MEM_RESERVE    = 0x2000
	_MEM_RELEASE    = 0x8000
	_PAGE_READWRITE = 0x04
	_PAGE_EXECUTE_READWRITE = 0x40
)

// Windows wait constants
const (
	_INFINITE         = 0xFFFFFFFF
	_WAIT_OBJECT_0    = 0
	_WAIT_TIMEOUT     = 0x102
	_WAIT_FAILED      = 0xFFFFFFFF
)

// Windows API function declarations (assembly in sys_cosmo_windows_amd64.s)
func winExitProcess(code uint32)
func winWriteFile(handle uintptr, buf *byte, n int32) int32
func winReadFile(handle uintptr, buf *byte, n int32) int32
func winGetStdHandle(nStdHandle int32) uintptr
func winCloseHandle(handle uintptr) bool
func winVirtualAlloc(addr uintptr, size uintptr, alloctype uint32, protect uint32) uintptr
func winVirtualFree(addr uintptr, size uintptr, freetype uint32) bool
func winGetCurrentThreadId() uint32
func winGetCurrentProcessId() uint32
func winSleep(ms uint32)
func winQueryPerformanceCounter() int64
func winQueryPerformanceFrequency() int64
func winTlsAlloc() uint32
func winTlsSetValue(slot uint32, value uintptr) bool
func winTlsGetValue(slot uint32) uintptr
func winCreateThread(fn uintptr, arg uintptr) uintptr
func winWaitForSingleObject(handle uintptr, ms uint32) uint32
func winCreateEventW(manual bool, initial bool) uintptr
func winSetEvent(handle uintptr) bool
func winResetEvent(handle uintptr) bool

// Windows standard handle cache
var (
	windowsStdin  uintptr
	windowsStdout uintptr
	windowsStderr uintptr
)

// Windows performance counter frequency (for time functions)
var windowsQPCFrequency int64

// initWindows initializes Windows-specific runtime state.
// Called early in runtime startup when iswindows == 1.
//
//go:nosplit
func initWindows() {
	if iswindows == 0 {
		return
	}

	// Get standard handles
	windowsStdin = winGetStdHandle(_STD_INPUT_HANDLE)
	windowsStdout = winGetStdHandle(_STD_OUTPUT_HANDLE)
	windowsStderr = winGetStdHandle(_STD_ERROR_HANDLE)

	// Get QPC frequency for time functions
	windowsQPCFrequency = winQueryPerformanceFrequency()

	// Allocate TLS slot for G pointer
	windowstlsslot = winTlsAlloc()
}

// windowsWrite writes to a Windows file handle.
//
//go:nosplit
func windowsWrite(fd uintptr, p unsafe.Pointer, n int32) int32 {
	var handle uintptr
	switch fd {
	case 1:
		handle = windowsStdout
	case 2:
		handle = windowsStderr
	default:
		handle = fd
	}
	return winWriteFile(handle, (*byte)(p), n)
}

// windowsRead reads from a Windows file handle.
//
//go:nosplit
func windowsRead(fd int32, p unsafe.Pointer, n int32) int32 {
	var handle uintptr
	if fd == 0 {
		handle = windowsStdin
	} else {
		handle = uintptr(fd)
	}
	return winReadFile(handle, (*byte)(p), n)
}

// windowsExit terminates the process.
//
//go:nosplit
func windowsExit(code int32) {
	winExitProcess(uint32(code))
}

// windowsUsleep sleeps for the specified number of microseconds.
//
//go:nosplit
func windowsUsleep(usec uint32) {
	winSleep(usec / 1000)
}

// windowsNanotime returns monotonic time in nanoseconds.
//
//go:nosplit
func windowsNanotime() int64 {
	counter := winQueryPerformanceCounter()
	// Convert to nanoseconds: counter * 1e9 / frequency
	return counter * 1000000000 / windowsQPCFrequency
}

// windowsMmap allocates memory using VirtualAlloc.
//
//go:nosplit
func windowsMmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) (unsafe.Pointer, int) {
	// Map Linux mmap flags to Windows VirtualAlloc
	alloctype := uint32(_MEM_COMMIT | _MEM_RESERVE)
	protect := uint32(_PAGE_READWRITE)

	// Handle execute permission
	if prot&_PROT_EXEC != 0 {
		protect = _PAGE_EXECUTE_READWRITE
	}

	p := winVirtualAlloc(uintptr(addr), n, alloctype, protect)
	if p == 0 {
		return nil, int(_ENOMEM)
	}
	return unsafe.Pointer(p), 0
}

// windowsMunmap frees memory using VirtualFree.
//
//go:nosplit
func windowsMunmap(addr unsafe.Pointer, n uintptr) {
	winVirtualFree(uintptr(addr), 0, _MEM_RELEASE)
}

// windowsGettid returns the current thread ID.
//
//go:nosplit
func windowsGettid() uint32 {
	return winGetCurrentThreadId()
}

// windowsGetpid returns the current process ID.
//
//go:nosplit
func windowsGetpid() int {
	return int(winGetCurrentProcessId())
}

// Windows TLS operations for G pointer

//go:nosplit
func windowsSettls(g uintptr) {
	winTlsSetValue(windowstlsslot, g)
}

//go:nosplit
func windowsGettls() uintptr {
	return winTlsGetValue(windowstlsslot)
}

// windowsSemaphore implements a simple semaphore using Windows events.
type windowsSemaphore struct {
	handle uintptr
}

//go:nosplit
func (s *windowsSemaphore) init() {
	s.handle = winCreateEventW(false, false)
}

//go:nosplit
func (s *windowsSemaphore) wait() {
	winWaitForSingleObject(s.handle, _INFINITE)
}

//go:nosplit
func (s *windowsSemaphore) wake() {
	winSetEvent(s.handle)
}
