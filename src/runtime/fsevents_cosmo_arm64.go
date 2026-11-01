// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// The macOS calls under the syscall package's inotify emulation. CoreServices
// is loaded the first time a program asks for it. cosmo_fsevents_callback,
// which runs on a libdispatch thread with no g, writes each event to the pipe
// it was given.

package runtime

import (
	"internal/abi"
	"unsafe"
)

const (
	_RTLD_NOW    = 0x2
	_RTLD_GLOBAL = 0x8

	_FSEVENT_SINCE_NOW = ^uintptr(0)
	_FSEVENT_NO_DEFER  = 0x2
	_FSEVENT_FILES     = 0x10
	_CF_ENCODING_UTF8  = 0x08000100

	_DARWIN_O_EVTONLY = 0x8000
	_DARWIN_O_SYMLINK = 0x200000
	_DARWIN_O_CLOEXEC = 0x1000000
	_DARWIN_F_GETPATH = 50
)

// A cosmoCCall is one call into C. The offsets are known to
// cosmo_ccall_trampoline.
type cosmoCCall struct {
	fn    uintptr
	args  [6]uintptr
	float float64
	ret   uintptr
}

func cosmo_ccall_trampoline()

func cosmo_fsevents_callback()

func cosmo_fsevents_noop()

// cosmoCall runs a call on the thread's stack, in syscall state, so a slow call
// holds up no other goroutine.
//
//go:nosplit
func cosmoCall(call *cosmoCCall) uintptr {
	entersyscall()
	asmcgocall(unsafe.Pointer(abi.FuncPCABI0(cosmo_ccall_trampoline)), unsafe.Pointer(call))
	exitsyscall()
	return call.ret
}

// cosmoC is nosplit because an argument can be the address of a stack variable.
//
//go:nosplit
func cosmoC(fn, arg0, arg1, arg2, arg3, arg4, arg5 uintptr) uintptr {
	call := cosmoCCall{fn: fn, args: [6]uintptr{arg0, arg1, arg2, arg3, arg4, arg5}}
	return cosmoCall(&call)
}

// The addresses cosmo_fsevents_callback reads.
var (
	cosmoFSEventStrlen uintptr
	cosmoFSEventWrite  uintptr
)

var fseventFns struct {
	streamCreate     uintptr
	streamSetQueue   uintptr
	streamStart      uintptr
	streamStop       uintptr
	streamInvalidate uintptr
	streamRelease    uintptr
	stringCreate     uintptr
	arrayCreate      uintptr
	arrayCallbacks   uintptr
	release          uintptr
	queueCreate      uintptr
	queueSync        uintptr
	queueRelease     uintptr
	open             uintptr
	fcntl            uintptr
	close            uintptr
}

var fseventLibrary = []byte("/System/Library/Frameworks/CoreServices.framework/CoreServices\x00")

var fseventSymbols = []struct {
	name []byte
	slot *uintptr
}{
	{[]byte("FSEventStreamCreate\x00"), &fseventFns.streamCreate},
	{[]byte("FSEventStreamSetDispatchQueue\x00"), &fseventFns.streamSetQueue},
	{[]byte("FSEventStreamStart\x00"), &fseventFns.streamStart},
	{[]byte("FSEventStreamStop\x00"), &fseventFns.streamStop},
	{[]byte("FSEventStreamInvalidate\x00"), &fseventFns.streamInvalidate},
	{[]byte("FSEventStreamRelease\x00"), &fseventFns.streamRelease},
	{[]byte("CFStringCreateWithCString\x00"), &fseventFns.stringCreate},
	{[]byte("CFArrayCreate\x00"), &fseventFns.arrayCreate},
	{[]byte("kCFTypeArrayCallBacks\x00"), &fseventFns.arrayCallbacks},
	{[]byte("CFRelease\x00"), &fseventFns.release},
	{[]byte("dispatch_queue_create\x00"), &fseventFns.queueCreate},
	{[]byte("dispatch_sync_f\x00"), &fseventFns.queueSync},
	{[]byte("dispatch_release\x00"), &fseventFns.queueRelease},
	{[]byte("open\x00"), &fseventFns.open},
	{[]byte("fcntl\x00"), &fseventFns.fcntl},
	{[]byte("close\x00"), &fseventFns.close},
	{[]byte("strlen\x00"), &cosmoFSEventStrlen},
	{[]byte("write\x00"), &cosmoFSEventWrite},
}

// syscall_cosmoFSEventInit loads CoreServices and reports whether every call
// was found. The caller runs it once.
//
//go:linkname syscall_cosmoFSEventInit syscall.cosmoFSEventInit
func syscall_cosmoFSEventInit() bool {
	lib := __syslib
	if lib == nil || lib.version < 6 || lib.dlopen == 0 || lib.dlsym == 0 {
		return false
	}
	path := uintptr(unsafe.Pointer(&fseventLibrary[0]))
	if cosmoC(lib.dlopen, path, _RTLD_NOW|_RTLD_GLOBAL, 0, 0, 0, 0) == 0 {
		return false
	}
	for _, symbol := range fseventSymbols {
		*symbol.slot = cosmoC(lib.dlsym, _RTLD_DEFAULT, uintptr(unsafe.Pointer(&symbol.name[0])), 0, 0, 0, 0)
		if *symbol.slot == 0 {
			return false
		}
	}
	return true
}

// syscall_cosmoFSEventQueue makes the serial queue an instance's streams run on.
//
//go:linkname syscall_cosmoFSEventQueue syscall.cosmoFSEventQueue
func syscall_cosmoFSEventQueue() uintptr {
	return cosmoC(fseventFns.queueCreate, 0, 0, 0, 0, 0, 0)
}

// syscall_cosmoFSEventDrain waits for the callback running on queue, if one is,
// and lets the queue go. Nothing may be scheduled on it any more.
//
//go:linkname syscall_cosmoFSEventDrain syscall.cosmoFSEventDrain
func syscall_cosmoFSEventDrain(queue uintptr) {
	cosmoC(fseventFns.queueSync, queue, 0, abi.FuncPCABI0(cosmo_fsevents_noop), 0, 0, 0)
	cosmoC(fseventFns.queueRelease, queue, 0, 0, 0, 0, 0)
}

// syscall_cosmoFSEventOpen starts a stream of a directory's file events and
// reports it, or zero. A stream with no latency delivers each event as it
// happens. The callback writes them to fd.
//
//go:linkname syscall_cosmoFSEventOpen syscall.cosmoFSEventOpen
func syscall_cosmoFSEventOpen(path *byte, fd int32, queue uintptr) uintptr {
	text := cosmoC(fseventFns.stringCreate, 0, uintptr(unsafe.Pointer(path)), _CF_ENCODING_UTF8, 0, 0, 0)
	if text == 0 {
		return 0
	}
	paths := cosmoC(fseventFns.arrayCreate, 0, uintptr(unsafe.Pointer(&text)), 1, fseventFns.arrayCallbacks, 0, 0)
	cosmoC(fseventFns.release, text, 0, 0, 0, 0, 0)
	if paths == 0 {
		return 0
	}
	context := [5]uintptr{0, uintptr(fd)}
	create := cosmoCCall{fn: fseventFns.streamCreate, args: [6]uintptr{
		0, abi.FuncPCABI0(cosmo_fsevents_callback), uintptr(unsafe.Pointer(&context)), paths,
		_FSEVENT_SINCE_NOW, _FSEVENT_FILES | _FSEVENT_NO_DEFER,
	}}
	stream := cosmoCall(&create)
	cosmoC(fseventFns.release, paths, 0, 0, 0, 0, 0)
	if stream == 0 {
		return 0
	}
	cosmoC(fseventFns.streamSetQueue, stream, queue, 0, 0, 0, 0)
	if uint8(cosmoC(fseventFns.streamStart, stream, 0, 0, 0, 0, 0)) == 0 {
		syscall_cosmoFSEventClose(stream)
		return 0
	}
	return stream
}

// syscall_cosmoFSEventClose stops a stream and lets it go.
//
//go:linkname syscall_cosmoFSEventClose syscall.cosmoFSEventClose
func syscall_cosmoFSEventClose(stream uintptr) {
	cosmoC(fseventFns.streamStop, stream, 0, 0, 0, 0, 0)
	cosmoC(fseventFns.streamInvalidate, stream, 0, 0, 0, 0, 0)
	cosmoC(fseventFns.streamRelease, stream, 0, 0, 0, 0, 0)
}

// syscall_cosmoFSEventPath writes the path macOS reports a file under to out,
// which holds MAXPATHLEN bytes. With follow false a final symlink is the file.
//
//go:linkname syscall_cosmoFSEventPath syscall.cosmoFSEventPath
func syscall_cosmoFSEventPath(path *byte, follow bool, out *byte) bool {
	flags := uintptr(_DARWIN_O_EVTONLY | _DARWIN_O_CLOEXEC)
	if !follow {
		flags |= _DARWIN_O_SYMLINK
	}
	fd := cosmoLibcCallVariadic1(fseventFns.open, uintptr(unsafe.Pointer(path)), flags, 0)
	if int32(fd) < 0 {
		return false
	}
	failed := cosmoLibcCallVariadic1(fseventFns.fcntl, fd, _DARWIN_F_GETPATH, uintptr(unsafe.Pointer(out)))
	cosmoLibcCall6(fseventFns.close, fd, 0, 0, 0, 0, 0)
	return int32(failed) == 0
}
