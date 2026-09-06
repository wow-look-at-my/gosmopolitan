// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// struct termios, both shapes, and the translation between them.
//
// Nothing lines up in this family, and two collisions are dangerous
// rather than merely wrong: Linux IXON (0x400) is Apple IXOFF, and Linux
// IUCLC (0x200) is Apple IXON. A forwarded flag word does not fail, it
// turns flow control inside out.
//
// Architecture-neutral on purpose - Apple numbers these identically on
// both its architectures - so the tests run wherever cosmo tests run.

// DarwinTermios is Apple's struct termios: 72 bytes.
type DarwinTermios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [20]uint8
	Ispeed uint64
	Ospeed uint64
}

// LinuxTermios is the struct the Linux TCGETS/TCSETS ioctls read and
// write: 36 bytes, and NOT the larger termios2. A caller may hand over a
// bigger buffer (x/sys/unix's Termios carries two speed fields the kernel
// never touches under TCGETS), so the emulation writes exactly these 36
// bytes and no more - the same bytes the kernel writes.
type LinuxTermios struct {
	Iflag uint32
	Oflag uint32
	Cflag uint32
	Lflag uint32
	Line  uint8
	Cc    [19]uint8
}

// termiosBit is one flag that both systems have, at different positions.
type termiosBit struct {
	linux uint32
	apple uint64
}

// Input flags. Linux IUCLC (0x200) and the case-mapping it asks for have
// no Apple counterpart and are absent here by design: 0x200 is Apple's
// IXON, so forwarding it would turn "map upper case to lower" into "start
// output flow control".
var termiosIflag = [...]termiosBit{
	{0x1, 0x1},       // IGNBRK
	{0x2, 0x2},       // BRKINT
	{0x4, 0x4},       // IGNPAR
	{0x8, 0x8},       // PARMRK
	{0x10, 0x10},     // INPCK
	{0x20, 0x20},     // ISTRIP
	{0x40, 0x40},     // INLCR
	{0x80, 0x80},     // IGNCR
	{0x100, 0x100},   // ICRNL
	{0x400, 0x200},   // IXON
	{0x1000, 0x400},  // IXOFF
	{0x800, 0x800},   // IXANY
	{0x2000, 0x2000}, // IMAXBEL
	{0x4000, 0x4000}, // IUTF8
}

// Output flags. Linux OLCUC and the delay fields (NLDLY, CRDLY, TABDLY,
// BSDLY, VTDLY, FFDLY) have no Apple counterpart; Apple's OXTABS and
// ONOEOT have no Linux one.
var termiosOflag = [...]termiosBit{
	{0x1, 0x1},      // OPOST
	{0x4, 0x2},      // ONLCR
	{0x8, 0x10},     // OCRNL
	{0x10, 0x20},    // ONOCR
	{0x20, 0x40},    // ONLRET
	{0x40, 0x80},    // OFILL
	{0x80, 0x20000}, // OFDEL
}

// Control flags, minus the character size and the baud rate, which are
// fields rather than bits and are converted separately. Linux CMSPAR
// (stick parity) has no Apple counterpart.
var termiosCflag = [...]termiosBit{
	{0x40, 0x400},         // CSTOPB
	{0x80, 0x800},         // CREAD
	{0x100, 0x1000},       // PARENB
	{0x200, 0x2000},       // PARODD
	{0x400, 0x4000},       // HUPCL
	{0x800, 0x8000},       // CLOCAL
	{0x80000000, 0x30000}, // CRTSCTS (Apple: CCTS_OFLOW|CRTS_IFLOW)
}

// Local flags. Linux XCASE has no Apple counterpart; Apple's ALTWERASE,
// NOKERNINFO and CIGNORE have no Linux one.
var termiosLflag = [...]termiosBit{
	{0x1, 0x80},          // ISIG
	{0x2, 0x100},         // ICANON
	{0x8, 0x8},           // ECHO
	{0x10, 0x2},          // ECHOE
	{0x20, 0x4},          // ECHOK
	{0x40, 0x10},         // ECHONL
	{0x80, 0x80000000},   // NOFLSH
	{0x100, 0x400000},    // TOSTOP
	{0x200, 0x40},        // ECHOCTL
	{0x400, 0x20},        // ECHOPRT
	{0x800, 0x1},         // ECHOKE
	{0x1000, 0x800000},   // FLUSHO
	{0x4000, 0x20000000}, // PENDIN
	{0x8000, 0x400},      // IEXTEN
	{0x10000, 0x800},     // EXTPROC
}

// Character size. CSIZE is a two-bit field, not a set of flags: Linux
// keeps it at 0x30, Apple at 0x300.
const (
	linuxCSIZE = 0x30
	appleCSIZE = 0x300
	csizeShift = 4
)

// Control characters. The index is the Linux slot, the value is Apple's;
// -1 marks a Linux slot Apple does not have (VSWTC, the switch character
// of a long-dead multiplexing driver).
var termiosCcIndex = [19]int8{
	0:  8,  // VINTR
	1:  9,  // VQUIT
	2:  3,  // VERASE
	3:  5,  // VKILL
	4:  0,  // VEOF
	5:  17, // VTIME
	6:  16, // VMIN
	7:  -1, // VSWTC
	8:  12, // VSTART
	9:  13, // VSTOP
	10: 10, // VSUSP
	11: 1,  // VEOL
	12: 6,  // VREPRINT
	13: 15, // VDISCARD
	14: 4,  // VWERASE
	15: 14, // VLNEXT
	16: 2,  // VEOL2
}

// Baud rates. Linux names a rate with a small code inside c_cflag; Apple
// stores the rate itself. A rate with no Linux code cannot be encoded,
// and the caller is told so rather than handed a plausible wrong number.
type termiosBaud struct {
	code uint32
	rate uint64
}

var termiosBauds = [...]termiosBaud{
	{0x0, 0},         // B0 (hang up)
	{0x1, 50},        // B50
	{0x2, 75},        // B75
	{0x3, 110},       // B110
	{0x4, 134},       // B134
	{0x5, 150},       // B150
	{0x6, 200},       // B200
	{0x7, 300},       // B300
	{0x8, 600},       // B600
	{0x9, 1200},      // B1200
	{0xa, 1800},      // B1800
	{0xb, 2400},      // B2400
	{0xc, 4800},      // B4800
	{0xd, 9600},      // B9600
	{0xe, 19200},     // B19200
	{0xf, 38400},     // B38400
	{0x1001, 57600},  // B57600
	{0x1002, 115200}, // B115200
	{0x1003, 230400}, // B230400
}

// linuxCBAUD is the mask the output rate's code occupies in c_cflag, and
// linuxCIBAUD the input rate's. An input rate of zero means "same as the
// output rate", which is what almost every caller leaves it at.
const (
	linuxCBAUD  = 0x100f
	linuxCIBAUD = 0x100f0000
	cibaudShift = 16
)

// DarwinBaudToLinux maps an Apple line rate to its Linux c_cflag code.
func DarwinBaudToLinux(rate uint64) (uint32, bool) {
	for _, b := range termiosBauds {
		if b.rate == rate {
			return b.code, true
		}
	}
	return 0, false
}

// DarwinBaudFromLinux maps a Linux c_cflag baud code to the rate itself.
func DarwinBaudFromLinux(code uint32) (uint64, bool) {
	for _, b := range termiosBauds {
		if b.code == code {
			return b.rate, true
		}
	}
	return 0, false
}

// DarwinTermiosToLinux converts what Apple's TIOCGETA filled into what a
// TCGETS caller expects. It reports false when Apple names a line rate
// with no Linux code: an unencodable speed is refused rather than
// reported as some other speed.
//
// Nothing is invented. A bit Apple has and Linux does not (ALTWERASE,
// NOKERNINFO, ONOEOT, OXTABS) has nowhere to go and is dropped, and every
// Linux bit whose Apple counterpart is clear stays clear.
func DarwinTermiosToLinux(src *DarwinTermios, dst *LinuxTermios) bool {
	dst.Iflag = appleBitsToLinux(src.Iflag, termiosIflag[:])
	dst.Oflag = appleBitsToLinux(src.Oflag, termiosOflag[:])
	dst.Cflag = appleBitsToLinux(src.Cflag, termiosCflag[:])
	dst.Lflag = appleBitsToLinux(src.Lflag, termiosLflag[:])

	// The character size is a two-bit FIELD, not a set of flags: Apple
	// keeps it four bits to the left of where Linux does.
	dst.Cflag |= uint32((src.Cflag&appleCSIZE)>>csizeShift) & linuxCSIZE

	// Line discipline: Linux reports N_TTY, the only one a terminal ever
	// has here. Apple has no such field.
	dst.Line = 0

	for l, a := range termiosCcIndex {
		if a < 0 {
			dst.Cc[l] = 0
			continue
		}
		dst.Cc[l] = src.Cc[a]
	}

	ocode, ok := DarwinBaudToLinux(src.Ospeed)
	if !ok {
		return false
	}
	dst.Cflag |= ocode
	if src.Ispeed != src.Ospeed {
		icode, ok := DarwinBaudToLinux(src.Ispeed)
		if !ok {
			return false
		}
		dst.Cflag |= icode << cibaudShift
	}
	return true
}

// DarwinTermiosFromLinux converts what a TCSETS caller passed into what
// Apple's TIOCSETA wants. It reports false for a baud code Apple has no
// rate for.
//
// dst is READ as well as written: every bit Linux cannot name (ALTWERASE,
// NOKERNINFO, ONOEOT, OXTABS) keeps the value it already had, so a
// get-modify-set does not clear settings it never knew were there.
//
// The Linux-only flags (IUCLC, OLCUC, XCASE, CMSPAR, the output delays)
// are dropped rather than failing the call: Linux leaves them to the
// driver, and no driver in use implements any of them.
func DarwinTermiosFromLinux(src *LinuxTermios, dst *DarwinTermios) bool {
	dst.Iflag = mergeLinuxBits(dst.Iflag, src.Iflag, termiosIflag[:])
	dst.Oflag = mergeLinuxBits(dst.Oflag, src.Oflag, termiosOflag[:])
	dst.Cflag = mergeLinuxBits(dst.Cflag, src.Cflag, termiosCflag[:])
	dst.Lflag = mergeLinuxBits(dst.Lflag, src.Lflag, termiosLflag[:])

	dst.Cflag &^= appleCSIZE
	dst.Cflag |= (uint64(src.Cflag) & linuxCSIZE) << csizeShift

	for l, a := range termiosCcIndex {
		if a < 0 {
			continue
		}
		dst.Cc[a] = src.Cc[l]
	}

	orate, ok := DarwinBaudFromLinux(src.Cflag & linuxCBAUD)
	if !ok {
		return false
	}
	dst.Ospeed = orate
	dst.Ispeed = orate
	if icode := (src.Cflag & linuxCIBAUD) >> cibaudShift; icode != 0 {
		irate, ok := DarwinBaudFromLinux(icode)
		if !ok {
			return false
		}
		dst.Ispeed = irate
	}
	return true
}

func appleBitsToLinux(v uint64, tab []termiosBit) uint32 {
	var out uint32
	for _, b := range tab {
		if v&b.apple == b.apple {
			out |= b.linux
		}
	}
	return out
}

// mergeLinuxBits rewrites every Apple bit this table knows about from the
// Linux word, and leaves the rest of cur alone - those are the settings
// the Linux caller could not see and must not clobber.
func mergeLinuxBits(cur uint64, v uint32, tab []termiosBit) uint64 {
	for _, b := range tab {
		cur &^= b.apple
	}
	for _, b := range tab {
		if v&b.linux == b.linux {
			cur |= b.apple
		}
	}
	return cur
}
