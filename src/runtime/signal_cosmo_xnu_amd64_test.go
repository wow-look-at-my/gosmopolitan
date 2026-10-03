// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime_test

import (
	. "runtime"
	"testing"
)

// The XNU x86-64 signal-frame layouts are wire formats the kernel writes
// and sigctxt reads, so a field that moves is silent corruption rather
// than a build failure. Expected values: user_ucontext64,
// user64_sigaltstack and user64_siginfo (XNU bsd/sys/signal.h,
// bsd/sys/_types/_ucontext64.h), x86_exception_state64 and
// x86_thread_state64 (mach/i386/_structs.h); Go's pre-1.12 darwin port
// (go1.8 runtime/defs_darwin_amd64.go) carries the same numbers.
//
// The test names carry the TestCosmoSig prefix because that is the
// pattern CI runs the runtime's cosmo tests under.

func checkLayout(t *testing.T, what string, fields []struct {
	name      string
	got, want uintptr
}) {
	t.Helper()
	for _, f := range fields {
		if f.got != f.want {
			t.Errorf("%s %s = %d, want %d", what, f.name, f.got, f.want)
		}
	}
}

func TestCosmoSigXnuAmd64Ucontext(t *testing.T) {
	checkLayout(t, "xnuUcontext", []struct {
		name      string
		got, want uintptr
	}{
		{"size", XnuUcontextSize, 56},
		{"uc_onstack", XnuUcontextOnstackOff, 0},
		{"uc_sigmask", XnuUcontextSigmaskOff, 4},
		{"uc_stack", XnuUcontextStackOff, 8},
		{"uc_link", XnuUcontextLinkOff, 32},
		{"uc_mcsize", XnuUcontextMcsizeOff, 40},
		{"uc_mcontext64", XnuUcontextMcontextOf, 48},
	})
	checkLayout(t, "xnuStackt", []struct {
		name      string
		got, want uintptr
	}{
		{"size", XnuStacktSize, 24},
		{"ss_sp", XnuStacktSpOff, 0},
		{"ss_size", XnuStacktSizeOff, 8},
		{"ss_flags", XnuStacktFlagsOff, 16},
	})
	// The Linux stackt this is translated from keeps flags at 8 and
	// size at 16: the two fields swap places, which is why the raw
	// struct cannot be handed to XNU.
	checkLayout(t, "stackt (linux)", []struct {
		name      string
		got, want uintptr
	}{
		{"size", LinuxStacktSize, 24},
		{"ss_flags", LinuxStacktFlagsOff, 8},
		{"ss_size", LinuxStacktSizeOff, 16},
	})
	if XnuSSOnstack != 1 || XnuSSDisable != 4 || LinuxSSDisab != 2 {
		t.Errorf("SS flags: apple onstack %d disable %d, linux disable %d; want 1, 4, 2",
			XnuSSOnstack, XnuSSDisable, LinuxSSDisab)
	}
}

func TestCosmoSigXnuAmd64Mcontext(t *testing.T) {
	checkLayout(t, "xnuMcontext64", []struct {
		name      string
		got, want uintptr
	}{
		{"es size", XnuExceptionState64Size, 16},
		{"ss", XnuMcontext64SsOff, 16},
		{"size (es+ss)", XnuMcontext64Size, 16 + 21*8},
		{"ss size", XnuRegs64Size, 21 * 8},
	})
	checkLayout(t, "xnuRegs64 (from mcontext start)", []struct {
		name      string
		got, want uintptr
	}{
		{"rax", 16 + XnuRegs64RaxOff, 16},
		{"rbx", 16 + XnuRegs64RbxOff, 24},
		{"rcx", 16 + XnuRegs64RcxOff, 32},
		{"rdx", 16 + XnuRegs64RdxOff, 40},
		{"rdi", 16 + XnuRegs64RdiOff, 48},
		{"rsi", 16 + XnuRegs64RsiOff, 56},
		{"rbp", 16 + XnuRegs64RbpOff, 64},
		{"rsp", 16 + XnuRegs64RspOff, 72},
		{"r8", 16 + XnuRegs64R8Off, 80},
		{"r15", 16 + XnuRegs64R15Off, 136},
		{"rip", 16 + XnuRegs64RipOff, 144},
		{"rflags", 16 + XnuRegs64RflagsOff, 152},
		{"cs", 16 + XnuRegs64CsOff, 160},
		{"fs", 16 + XnuRegs64FsOff, 168},
		{"gs", 16 + XnuRegs64GsOff, 176},
	})
	// The Linux context the same accessors read on Linux hosts embeds
	// its registers by value; rip sits at 168 inside the ucontext.
	if got := LinuxUcontextMcontextOff + LinuxSigcontextRipOff; got != 168 {
		t.Errorf("linux ucontext rip offset = %d, want 168", got)
	}
}

func TestCosmoSigXnuAmd64Siginfo(t *testing.T) {
	checkLayout(t, "xnuSiginfo", []struct {
		name      string
		got, want uintptr
	}{
		{"size", XnuSiginfoSize, 104},
		{"si_signo", XnuSiginfoSignoOff, 0},
		{"si_errno", XnuSiginfoErrnoOff, 4},
		{"si_code", XnuSiginfoCodeOff, 8},
		{"si_pid", XnuSiginfoPidOff, 12},
		{"si_uid", XnuSiginfoUidOff, 16},
		{"si_status", XnuSiginfoStatusOff, 20},
		{"si_addr", XnuSiginfoAddrOff, 24},
		{"si_value", XnuSiginfoValueOff, 32},
		{"si_band", XnuSiginfoBandOff, 40},
	})
	if LinuxSiginfoAddrOff != 16 {
		t.Errorf("linux siginfo si_addr offset = %d, want 16", LinuxSiginfoAddrOff)
	}
}

// __sigaction takes the 24-byte kernel struct with sa_tramp and copies
// the OLD action out as the 16-byte user64_sigaction without it (XNU
// kern_sig.c sigaction_kern_to_user64).
func TestCosmoSigXnuAmd64Sigaction(t *testing.T) {
	checkLayout(t, "sigaction structs", []struct {
		name      string
		got, want uintptr
	}{
		{"xnuKsigactiont size", XnuKsigactiontSize, 24},
		{"xnuKsigactiont sa_tramp", XnuKsigactiontTrampOff, 8},
		{"xnuSigactiont size", XnuSigactiontSize, 16},
		{"xnuSigactiont sa_handler", XnuSigactiontHandlerOff, 0},
		{"xnuSigactiont sa_mask", XnuSigactiontMaskOff, 8},
		{"xnuSigactiont sa_flags", XnuSigactiontFlagsOff, 12},
	})
}

// Apple SIGFPE codes (go1.8 defs_darwin_amd64.go) to Linux's
// (defs_cosmo_amd64.go). sigpanic keys panicdivide on the Linux
// FPE_INTDIV.
func TestCosmoSigXnuAmd64FPECode(t *testing.T) {
	pairs := map[uint64]uint64{ // apple -> linux
		7: 1, // INTDIV
		8: 2, // INTOVF
		1: 3, // FLTDIV
		2: 4, // FLTOVF
		3: 5, // FLTUND
		4: 6, // FLTRES
		5: 7, // FLTINV
		6: 8, // FLTSUB
	}
	for a, l := range pairs {
		if got := XnuFPECodeA2L(a); got != l {
			t.Errorf("XnuFPECodeA2L(%d) = %d, want %d", a, got, l)
		}
	}
	for _, c := range []uint64{0, 9, 0x10001} {
		if got := XnuFPECodeA2L(c); got != c {
			t.Errorf("XnuFPECodeA2L(%d) = %d, want passthrough", c, got)
		}
	}
}

// errnoPairs is Apple errno -> Linux errno, name by name from
// syscall/zerrors_darwin_amd64.go and syscall/zerrors_linux_amd64.go.
// An Apple errno with no Linux name maps to the nearest Linux meaning;
// the choice is recorded next to each such entry.
var errnoPairs = map[uint32]uint32{
	1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6, 7: 7, 8: 8, 9: 9, 10: 10,
	11: 35, // EDEADLK
	12: 12, 13: 13, 14: 14, 15: 15, 16: 16, 17: 17, 18: 18, 19: 19, 20: 20,
	21: 21, 22: 22, 23: 23, 24: 24, 25: 25, 26: 26, 27: 27, 28: 28, 29: 29, 30: 30,
	31: 31, 32: 32, 33: 33, 34: 34,
	35:  11,  // EAGAIN / EWOULDBLOCK
	36:  115, // EINPROGRESS
	37:  114, // EALREADY
	38:  88,  // ENOTSOCK
	39:  89,  // EDESTADDRREQ
	40:  90,  // EMSGSIZE
	41:  91,  // EPROTOTYPE
	42:  92,  // ENOPROTOOPT
	43:  93,  // EPROTONOSUPPORT
	44:  94,  // ESOCKTNOSUPPORT
	45:  95,  // ENOTSUP
	46:  96,  // EPFNOSUPPORT
	47:  97,  // EAFNOSUPPORT
	48:  98,  // EADDRINUSE
	49:  99,  // EADDRNOTAVAIL
	50:  100, // ENETDOWN
	51:  101, // ENETUNREACH
	52:  102, // ENETRESET
	53:  103, // ECONNABORTED
	54:  104, // ECONNRESET
	55:  105, // ENOBUFS
	56:  106, // EISCONN
	57:  107, // ENOTCONN
	58:  108, // ESHUTDOWN
	59:  109, // ETOOMANYREFS
	60:  110, // ETIMEDOUT
	61:  111, // ECONNREFUSED
	62:  40,  // ELOOP
	63:  36,  // ENAMETOOLONG
	64:  112, // EHOSTDOWN
	65:  113, // EHOSTUNREACH
	66:  39,  // ENOTEMPTY
	67:  11,  // EPROCLIM: no Linux name; Linux reports process limits as EAGAIN
	68:  87,  // EUSERS
	69:  122, // EDQUOT
	70:  116, // ESTALE
	71:  66,  // EREMOTE
	72:  5,   // EBADRPC: no Linux name; EIO
	73:  5,   // ERPCMISMATCH: no Linux name; EIO
	74:  5,   // EPROGUNAVAIL: no Linux name; EIO
	75:  5,   // EPROGMISMATCH: no Linux name; EIO
	76:  5,   // EPROCUNAVAIL: no Linux name; EIO
	77:  37,  // ENOLCK
	78:  38,  // ENOSYS
	79:  22,  // EFTYPE: no Linux name; EINVAL
	80:  13,  // EAUTH: no Linux name; EACCES
	81:  13,  // ENEEDAUTH: no Linux name; EACCES
	82:  5,   // EPWROFF: no Linux name; EIO
	83:  5,   // EDEVERR: no Linux name; EIO
	84:  75,  // EOVERFLOW
	85:  8,   // EBADEXEC: no Linux name; ENOEXEC
	86:  8,   // EBADARCH: no Linux name; ENOEXEC
	87:  8,   // ESHLIBVERS: no Linux name; ENOEXEC
	88:  8,   // EBADMACHO: no Linux name; ENOEXEC
	89:  125, // ECANCELED
	90:  43,  // EIDRM
	91:  42,  // ENOMSG
	92:  84,  // EILSEQ
	93:  61,  // ENOATTR: no Linux name; ENODATA
	94:  74,  // EBADMSG
	95:  72,  // EMULTIHOP
	96:  61,  // ENODATA
	97:  67,  // ENOLINK
	98:  63,  // ENOSR
	99:  60,  // ENOSTR
	100: 71,  // EPROTO
	101: 62,  // ETIME
	102: 95,  // EOPNOTSUPP
	103: 22,  // ENOPOLICY: no Linux name; EINVAL
	104: 131, // ENOTRECOVERABLE
	105: 130, // EOWNERDEAD
	106: 22,  // EQFULL (past the tree's ELAST): no Linux name; EINVAL
}

func TestCosmoSigXnuAmd64ErrnoXlat(t *testing.T) {
	if got := CosmoXlatErrno(0); got != 0 {
		t.Errorf("CosmoXlatErrno(0) = %d, want 0", got)
	}
	for a := uint32(1); a <= 106; a++ {
		want, ok := errnoPairs[a]
		if !ok {
			t.Errorf("errnoPairs has no entry for Apple errno %d", a)
			continue
		}
		if got := CosmoXlatErrno(a); got != want {
			t.Errorf("CosmoXlatErrno(%d) = %d, want %d", a, got, want)
		}
	}
	// Past the table, the value passes through unchanged.
	for _, a := range []uint32{107, 200, 4095} {
		if got := CosmoXlatErrno(a); got != a {
			t.Errorf("CosmoXlatErrno(%d) = %d, want passthrough", a, got)
		}
	}
}
