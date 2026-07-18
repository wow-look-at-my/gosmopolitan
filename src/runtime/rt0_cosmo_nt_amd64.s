// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

// Windows NT boot stub for cosmo/amd64 APEs (wave 1).
//
// The APE's PE header (written by cmd/link's convertToAPE) maps the
// embedded cosmo amd64 image and points AddressOfEntryPoint at
// _rt0_cosmo_nt below. The header's import directory points at the
// runtime·ntidata blob, so by the time the NT loader transfers control
// here, runtime·ntiat holds real kernel32 function pointers (read at
// osArchInit by the NT personality's resolver, os_cosmo_nt.go). The
// stub marks the host as Windows, fabricates the SysV boot block the
// shared cosmo boot path expects, and joins _rt0_amd64.
//
// Everything here is pure asm: no g, no TLS, no Go calls, no win64
// calls even - the first foreign call happens later, in osArchInit.

#include "textflag.h"

// runtime·ntidata is the PE import machinery for the minimal cosmo
// import set: kernel32.dll -> GetProcAddress + LoadLibraryA. Everything
// else is resolved at runtime through those two. The blob has a fixed
// layout that cmd/link (ape.go) relies on; the linker patches the five
// RVA fields marked [PATCH] directly in the output file at convertToAPE
// time (they are zero here) and points the PE DataDirectory[1] at the
// blob. It lives in noptrdata (RW, file-backed): the loader reads the
// tables and writes the IAT in place, matching real Cosmopolitan's RW
// .idata.
//
//	0x00 u32 IDT[0].OriginalFirstThunk  [PATCH -> RVA(ntidata+0x28)]
//	0x04 u32 IDT[0].TimeDateStamp       (0)
//	0x08 u32 IDT[0].ForwarderChain      (0)
//	0x0C u32 IDT[0].Name                [PATCH -> RVA(ntidata+0x62)]
//	0x10 u32 IDT[0].FirstThunk          [PATCH -> RVA(runtime·ntiat)]
//	0x14 IDT terminator entry           (20 zero bytes)
//	0x28 u64 ILT[0]                     [PATCH -> RVA(ntidata+0x40)]
//	0x30 u64 ILT[1]                     [PATCH -> RVA(ntidata+0x52)]
//	0x38 u64 ILT terminator             (0)
//	0x40 u16 hint 0, "GetProcAddress\0" (2-aligned hint/name entry)
//	0x51 zero pad
//	0x52 u16 hint 0, "LoadLibraryA\0"   (2-aligned hint/name entry)
//	0x61 zero pad
//	0x62 "kernel32.dll\0"
//	0x6F zero pad to 0x70
DATA runtime·ntidata+0x42(SB)/8, $"GetProcA"
DATA runtime·ntidata+0x4a(SB)/4, $"ddre"
DATA runtime·ntidata+0x4e(SB)/2, $"ss"
DATA runtime·ntidata+0x54(SB)/8, $"LoadLibr"
DATA runtime·ntidata+0x5c(SB)/4, $"aryA"
DATA runtime·ntidata+0x62(SB)/8, $"kernel32"
DATA runtime·ntidata+0x6a(SB)/4, $".dll"
GLOBL runtime·ntidata(SB), NOPTR, $0x70

// runtime·ntiat is the import address table the NT loader fills in
// before entry (IDT[0].FirstThunk points here). Slot order must match
// the ILT order in runtime·ntidata:
//
//	+0  &kernel32!GetProcAddress
//	+8  &kernel32!LoadLibraryA
//	+16 terminator (0)
//
// Slots 0 and 1 hold a nonzero placeholder so the symbol is
// file-backed initialized data (noptrdata, not BSS): the loader
// overwrites bytes that exist in the file.
DATA runtime·ntiat+0(SB)/8, $1
DATA runtime·ntiat+8(SB)/8, $1
GLOBL runtime·ntiat(SB), NOPTR, $24

// x87 control word for the FLDCW re-init below: round to nearest,
// 64-bit (long double) precision, all exceptions masked. Same value
// Cosmopolitan's crt loads at boot.
DATA ntfpucw<>+0(SB)/2, $0x37f
GLOBL ntfpucw<>(SB), RODATA|NOPTR, $2

// ntbootrand is the 16-byte buffer the boot stub fills with
// RDTSC-derived entropy and the fabricated AT_RANDOM points at
// (sysauxv aims startupRand here). DATA-initialized so the symbol is
// file-backed noptrdata rather than BSS - bytes that exist in the
// file, same belt-and-braces as ntiat.
DATA runtime·ntbootrand+0(SB)/8, $1
DATA runtime·ntbootrand+8(SB)/8, $1
GLOBL runtime·ntbootrand(SB), NOPTR, $16

// ntargv0 is the static argv[0] of the fabricated boot block: "APE\0".
// goargs replaces the whole argv with the GetCommandLineW parse on NT
// (os_cosmo_nt.go cosmoNTGoargs), so this only surfaces if that parse
// comes back empty.
DATA ntargv0<>+0(SB)/4, $0x00455041 // 'A' 'P' 'E' '\0', little-endian
GLOBL ntargv0<>(SB), RODATA|NOPTR, $4

// _rt0_cosmo_nt is the PE AddressOfEntryPoint. The NT loader calls it
// win64-style: args in CX/DX/R8/R9, 32 bytes of caller-allocated
// shadow space above the return address, RSP == 8 (mod 16) on entry.
//
// The stub hands the machine to the ordinary cosmo runtime boot:
//
//	1. x87 re-init (NT boots the FPU in 53-bit mode).
//	2. __hostos = _HOSTWINDOWS. Entering via the PE entry point
//	   means the host IS NT - real cosmo's WinMain trick ("you KNOW
//	   you're on NT because you entered via WinMain"). The unix rt0
//	   (rt0_cosmo_amd64.s) receives this in CL from the APE loader;
//	   here it is a constant.
//	3. Fill ntbootrand with boot entropy for AT_RANDOM.
//	4. Fabricate the SysV boot block sysargs walks (os_cosmo.go):
//	   argc/argv/NULL/envp-NULL/auxv on the OS stack.
//	5. JMP _rt0_amd64 (asm_amd64.s), which loads argc/argv from
//	   0(SP)/8(SP) and falls into rt0_go. The NT function table is
//	   resolved from the loader-filled ntiat slots at osArchInit.
//
// The keyhole tests in pe_test.go/ape_test.go assert the first three
// instructions (cld; fldcw; movl $2, __hostos) - update them if this
// prologue changes.
TEXT _rt0_cosmo_nt(SB),NOSPLIT|NOFRAME,$0
	CLD
	// Windows initializes the x87 FPU to 53-bit (double) precision;
	// the APE spec's Windows section says programs detecting NT must
	// fldcw a control word restoring 64-bit long-double mode
	// (ape/specification.md, "The x87 FPU control word").
	FLDCW	ntfpucw<>(SB)

	// __hostos = _HOSTWINDOWS (os_cosmo_amd64.go). From here on the
	// runtime's iswindows()/CHECK_WINDOWS branches are live and no
	// raw Linux SYSCALL may execute.
	MOVL	$2, runtime·__hostos(SB)

	// 16 bytes of RDTSC-derived entropy for AT_RANDOM: two timestamp
	// reads mixed with address bits (the NT stack and PEB locations
	// are ASLR'd, contributing a little). Weak, but only seeds boot
	// hashes; wave 2 upgrades this to ProcessPrng. RDTSC returns
	// EDX:EAX; CX still holds whatever the loader passed (the PEB on
	// current NT), worth folding in as extra address bits.
	RDTSC
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	SP, DX
	XORQ	DX, AX
	MOVQ	AX, runtime·ntbootrand+0(SB)
	RDTSC
	SHLQ	$32, DX
	ORQ	DX, AX
	ROLQ	$31, AX
	XORQ	CX, AX
	MOVQ	AX, runtime·ntbootrand+8(SB)

	// Fabricate the SysV boot block, built on the OS stack below the
	// entry SP (inside the committed 64KiB window our PE header
	// requests), 16-aligned with argc at the final 0(SP) - exactly
	// the layout _rt0_amd64 reads and sysargs walks:
	//
	//	 0(SP)	argc = 1
	//	 8(SP)	argv[0] -> "APE" (placeholder; see ntargv0)
	//	16(SP)	argv terminator NULL
	//	24(SP)	envp terminator NULL (goenvs's NT branch reads the
	//		real environment via GetEnvironmentStringsW)
	//	32(SP)	AT_PAGESZ (6)
	//	40(SP)	0x1000 (mallocinit throws if physPageSize == 0)
	//	48(SP)	AT_RANDOM (25)
	//	56(SP)	-> ntbootrand
	//	64(SP)	AT_NULL terminator pair
	//	72(SP)	0
	SUBQ	$112, SP
	ANDQ	$~15, SP
	MOVQ	$1, 0(SP)
	LEAQ	ntargv0<>(SB), AX
	MOVQ	AX, 8(SP)
	MOVQ	$0, 16(SP)
	MOVQ	$0, 24(SP)
	MOVQ	$6, 32(SP)
	MOVQ	$0x1000, 40(SP)
	MOVQ	$25, 48(SP)
	LEAQ	runtime·ntbootrand(SB), AX
	MOVQ	AX, 56(SP)
	MOVQ	$0, 64(SP)
	MOVQ	$0, 72(SP)

	// Join the common amd64 boot path. JMP, not CALL: _rt0_amd64
	// expects argc at 0(SP).
	JMP	_rt0_amd64(SB)
