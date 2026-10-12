// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// Linux <-> Apple signal number translation for the darwin syscall emulation.

// darwinXlatSignal translates a Linux signal number to Apple's. Only classic
// signals are mapped: Linux realtime signals (>=32) and SIGPWR/SIGSTKFLT have
// no Apple equivalent and report false.
//
//go:nosplit
func darwinXlatSignal(sig uintptr) (uintptr, bool) {
	switch sig {
	case 0, 1, 2, 3, 4, 5, 6, 8, 9, 11, 13, 14, 15, 21, 22, 24, 25, 26, 27, 28:
		return sig, true
	case 7: // SIGBUS
		return 10, true
	case 10: // SIGUSR1
		return 30, true
	case 12: // SIGUSR2
		return 31, true
	case 17: // SIGCHLD
		return 20, true
	case 18: // SIGCONT
		return 19, true
	case 19: // SIGSTOP
		return 17, true
	case 20: // SIGTSTP
		return 18, true
	case 23: // SIGURG
		return 16, true
	case 29: // SIGIO
		return 23, true
	case 31: // SIGSYS
		return 12, true
	}
	return 0, false
}

//go:nosplit
func darwinXlatWaitStatus(s uint32) uint32 {
	if s == 0xffff { // continued
		return s
	}
	if s&0xff == 0x7f {
		if l, ok := darwinXlatSignalA2L(uintptr(s >> 8 & 0xff)); ok {
			return 0x7f | uint32(l)<<8
		}
		return s
	}
	if t := s & 0x7f; t != 0 {
		if l, ok := darwinXlatSignalA2L(uintptr(t)); ok {
			return s&^uint32(0x7f) | uint32(l)
		}
	}
	return s // exited
}

// darwinXlatSignalA2L translates an Apple signal number to Linux's
// (the inverse of darwinXlatSignal).
//
//go:nosplit
func darwinXlatSignalA2L(sig uintptr) (uintptr, bool) {
	switch sig {
	case 0, 1, 2, 3, 4, 5, 6, 8, 9, 11, 13, 14, 15, 21, 22, 24, 25, 26, 27, 28:
		return sig, true
	case 10: // SIGBUS
		return 7, true
	case 30: // SIGUSR1
		return 10, true
	case 31: // SIGUSR2
		return 12, true
	case 20: // SIGCHLD
		return 17, true
	case 19: // SIGCONT
		return 18, true
	case 17: // SIGSTOP
		return 19, true
	case 18: // SIGTSTP
		return 20, true
	case 16: // SIGURG
		return 23, true
	case 23: // SIGIO
		return 29, true
	case 12: // SIGSYS
		return 31, true
	}
	return 0, false
}
