// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

// The Win32 calls under the syscall package's inotify emulation: directory
// handles reading through ReadDirectoryChangesW into one completion port.

package runtime

import "unsafe"

const (
	_NT_FILE_LIST_DIRECTORY    = 0x1
	_NT_FILE_FLAG_OVERLAPPED   = 0x40000000
	_NT_WATCH_NOTIFY_FILTER    = 0x1 | 0x2 | 0x4 | 0x8 | 0x10 | 0x40 // file and directory names, attributes, size, write and creation times
	_NT_FIONREAD               = 0x4004667F
	_NT_READ_DIRECTORY_SUBTREE = 0 // a watch covers one directory, as inotify's does
)

var (
	ntCreateIoCompletionPortFn     uintptr
	ntGetQueuedCompletionStatusFn  uintptr
	ntPostQueuedCompletionStatusFn uintptr
	ntReadDirectoryChangesWFn      uintptr

	ntNameCreateIoCompletionPort     = []byte("CreateIoCompletionPort\x00")
	ntNameGetQueuedCompletionStatus  = []byte("GetQueuedCompletionStatus\x00")
	ntNamePostQueuedCompletionStatus = []byte("PostQueuedCompletionStatus\x00")
	ntNameReadDirectoryChangesW      = []byte("ReadDirectoryChangesW\x00")
)

// ntResolveWatch fills the table from kernel32, for ntResolve.
func ntResolveWatch(k32sym func(*byte) uintptr) {
	ntCreateIoCompletionPortFn = k32sym(&ntNameCreateIoCompletionPort[0])
	ntGetQueuedCompletionStatusFn = k32sym(&ntNameGetQueuedCompletionStatus[0])
	ntPostQueuedCompletionStatusFn = k32sym(&ntNamePostQueuedCompletionStatus[0])
	ntReadDirectoryChangesWFn = k32sym(&ntNameReadDirectoryChangesW[0])
}

// ntFinalPath resolves path to its Linux spelling. An unfollowed path keeps a
// final symlink. It reports an errno.
func ntFinalPath(path string, follow bool) (string, uintptr) {
	w := ntPathW(path)
	if w == nil {
		return "", ntENOENT
	}
	flags := uintptr(_NT_FILE_FLAG_BACKUP_SEMANTICS)
	if !follow {
		flags |= _NT_FILE_FLAG_OPEN_REPARSE_POINT
	}
	h, werr := ntcallE(ntCreateFileWFn, uintptr(unsafe.Pointer(&w[0])), _NT_FILE_READ_ATTRIBUTES,
		_NT_FILE_SHARE_ALL, 0, _NT_OPEN_EXISTING, flags, 0)
	KeepAlive(w)
	if h == _NT_INVALID_HANDLE_VALUE {
		return "", ntErrno(werr)
	}
	final, eno := ntHandlePath(h)
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
	return final, eno
}

// ntHandlePath reports where a handle's file is now.
func ntHandlePath(h uintptr) (string, uintptr) {
	w, eno := ntHandlePathW(h)
	if eno != 0 {
		return "", eno
	}
	return ntPathToLinux(w[:len(w)-1]), 0
}

// ntWatchPort makes a completion port served by one thread.
func ntWatchPort() (port, errno uintptr) {
	port, werr := ntcallE(ntCreateIoCompletionPortFn, _NT_INVALID_HANDLE_VALUE, 0, 0, 1, 0, 0, 0)
	if port == 0 {
		return 0, ntErrno(werr)
	}
	return port, 0
}

// ntWatchOpen opens a directory for overlapped reads and ties it to port, where
// its completions arrive under key. Every sharing mode stays open, so the watch
// never stops the directory being renamed or removed.
func ntWatchOpen(path string, port, key uintptr) (handle, errno uintptr) {
	w := ntPathW(path)
	if w == nil {
		return 0, ntENOENT
	}
	h, werr := ntcallE(ntCreateFileWFn, uintptr(unsafe.Pointer(&w[0])), _NT_FILE_LIST_DIRECTORY,
		_NT_FILE_SHARE_ALL, 0, _NT_OPEN_EXISTING, _NT_FILE_FLAG_BACKUP_SEMANTICS|_NT_FILE_FLAG_OVERLAPPED, 0)
	KeepAlive(w)
	if h == _NT_INVALID_HANDLE_VALUE {
		return 0, ntErrno(werr)
	}
	if r, werr := ntcallE(ntCreateIoCompletionPortFn, h, port, key, 0, 0, 0, 0); r == 0 {
		ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
		return 0, ntErrno(werr)
	}
	return h, 0
}

// ntWatchRead starts one read of the directory's changes and reports a Win32
// error. buffer and overlapped stay in the kernel's hands until the read's
// completion comes out of the port.
func ntWatchRead(handle uintptr, buffer []byte, overlapped *[4]uintptr) uintptr {
	r, werr := ntcallSE10(ntReadDirectoryChangesWFn, handle, uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)), _NT_READ_DIRECTORY_SUBTREE, _NT_WATCH_NOTIFY_FILTER, 0,
		uintptr(unsafe.Pointer(overlapped)), 0, 0, 0)
	if r == 0 {
		return werr
	}
	return 0
}

// ntWatchWait takes the next completion from port. completed is false when the
// wait ran out, or a wake arrived. failure is the read's Win32 error.
func ntWatchWait(port uintptr, millis uint32) (transferred uint32, key uintptr, completed bool, failure uintptr) {
	var overlapped uintptr
	r, werr := ntcallSE(ntGetQueuedCompletionStatusFn, port, uintptr(unsafe.Pointer(&transferred)),
		uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&overlapped)), uintptr(millis), 0, 0)
	if overlapped == 0 {
		return 0, 0, false, 0
	}
	if r == 0 {
		return transferred, key, true, werr
	}
	return transferred, key, true, 0
}

// ntWatchWake makes a wait on port return with no completion.
func ntWatchWake(port uintptr) {
	ntcall(ntPostQueuedCompletionStatusFn, port, 0, 0, 0, 0, 0)
}

// ntWatchCancel ends the read in flight, whichever thread started it.
func ntWatchCancel(handle uintptr) {
	ntcall(ntCancelIoExFn, handle, 0, 0, 0, 0, 0)
}

func ntWatchClose(handle uintptr) {
	ntcall(ntCloseHandleFn, handle, 0, 0, 0, 0, 0)
}

// ntSocketUnread reports how many bytes wait to be read on a socket descriptor.
func ntSocketUnread(fd int32) (unread int, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return 0, eno
	}
	var count uint32
	r, werr := ntcallE(ntWSAIoctlsocketFn, e.handle, _NT_FIONREAD, uintptr(unsafe.Pointer(&count)), 0, 0, 0, 0)
	if ntSockErr(r) {
		return 0, ntWSAToLinux(werr)
	}
	return int(count), 0
}

// ntSocketAwait blocks in WSAPoll, which leaves the socket's other calls running
// where a blocking recv would stop them.
func ntSocketAwait(fd int32) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return
	}
	polled := ntWSAPollFD{fd: e.handle, events: _NT_POLLRDNORM}
	ntcallSE(ntWSAPollFn, uintptr(unsafe.Pointer(&polled)), 1, ^uintptr(0), 0, 0, 0, 0)
	KeepAlive(&polled)
}
