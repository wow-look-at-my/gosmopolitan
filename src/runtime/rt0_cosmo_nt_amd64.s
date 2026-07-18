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
// here, runtime·ntiat holds real kernel32 function pointers. Wave 1
// proves that whole chain end to end by exiting with code 42 through
// loader-resolved imports; later waves replace the ExitProcess tail
// with the real NT runtime personality boot.
//
// Everything here is pure asm: no g, no TLS, no Go calls, no stack
// beyond the small win64 shadow-space frame.

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

DATA ntstrkernel32<>+0(SB)/8, $"kernel32"
DATA ntstrkernel32<>+8(SB)/4, $".dll"
GLOBL ntstrkernel32<>(SB), RODATA|NOPTR, $16

DATA ntstrexitprocess<>+0(SB)/8, $"ExitProc"
DATA ntstrexitprocess<>+8(SB)/2, $"es"
DATA ntstrexitprocess<>+10(SB)/1, $0x73 // 's'
GLOBL ntstrexitprocess<>(SB), RODATA|NOPTR, $16

// _rt0_cosmo_nt is the PE AddressOfEntryPoint. The NT loader calls it
// win64-style: args in CX/DX/R8/R9, 32 bytes of caller-allocated
// shadow space above the return address, RSP == 8 (mod 16) on entry.
//
// Wave 1: prove the import chain works, then exit 42.
//
//	hk32 = LoadLibraryA("kernel32.dll")     // via IAT slot 1
//	fn   = GetProcAddress(hk32, "ExitProcess") // via IAT slot 0
//	fn(42)                                  // never returns
TEXT _rt0_cosmo_nt(SB),NOSPLIT|NOFRAME,$0
	CLD
	// Windows initializes the x87 FPU to 53-bit (double) precision;
	// the APE spec's Windows section says programs detecting NT must
	// fldcw a control word restoring 64-bit long-double mode
	// (ape/specification.md, "The x87 FPU control word").
	FLDCW	ntfpucw<>(SB)
	// Entry RSP == 8 (mod 16). Dropping 40 bytes realigns to 16 and
	// provides the 32-byte shadow space every win64 CALL requires.
	SUBQ	$40, SP

	// AX = LoadLibraryA("kernel32.dll"). kernel32 is already loaded
	// (our import table names it), so this just returns its HMODULE.
	LEAQ	ntstrkernel32<>(SB), CX
	MOVQ	runtime·ntiat+8(SB), AX
	CALL	AX

	// AX = GetProcAddress(hk32, "ExitProcess"). Reload every
	// argument register: all of CX/DX/R8-R11 are caller-saved and
	// were clobbered by the call above.
	MOVQ	AX, CX
	LEAQ	ntstrexitprocess<>(SB), DX
	MOVQ	runtime·ntiat+0(SB), AX
	CALL	AX

	// ExitProcess(42). Never returns.
	MOVL	$42, CX
	CALL	AX
	INT	$3
