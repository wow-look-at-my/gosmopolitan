// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

#include "textflag.h"

// Apple errno -> Linux errno, indexed by the Apple value (0..106).
//
// It carries no instructions, only data, so it lives in an
// architecture-neutral file rather than inside sys_cosmo_arm64.s: both
// return paths need the same 112 bytes, and a second copy would drift
// the first time anybody corrected an entry.
//
// Two register-convention readers wrap it, one per architecture:
// runtime·cosmo_xlat_errno_r0 (sys_cosmo_arm64.s, R0) and
// runtime·cosmo_xlat_errno_ax (sys_cosmo_amd64.s, AX). Each is pushed
// by a linkname declaration so the syscall packages can call it from
// their own assembly.
//
// Why the translation exists at all: a darwin host reports failure with
// APPLE errno numbers - the Syslib's libc calls return -errno on arm64,
// and a raw XNU syscall returns a positive errno with the carry flag set
// on amd64 - while Go compares against LINUX values (Errno, EAGAIN, and
// the rest). The first 34 agree; the BSD range diverges.
//
// Both names are given below where they differ:
//   1..10  identity (EPERM..ECHILD)
//  11 EDEADLK             -> 35    12..34 identity (ENOMEM..ERANGE)
//  35 EAGAIN/EWOULDBLOCK  -> 11    36 EINPROGRESS -> 115   37 EALREADY -> 114
//  38 ENOTSOCK  -> 88   39 EDESTADDRREQ -> 89   40 EMSGSIZE -> 90
//  41 EPROTOTYPE -> 91  42 ENOPROTOOPT -> 92    43 EPROTONOSUPPORT -> 93
//  44 ESOCKTNOSUPPORT -> 94  45 ENOTSUP -> 95 (EOPNOTSUPP)
//  46 EPFNOSUPPORT -> 96  47 EAFNOSUPPORT -> 97  48 EADDRINUSE -> 98
//  49 EADDRNOTAVAIL -> 99  50 ENETDOWN -> 100    51 ENETUNREACH -> 101
//  52 ENETRESET -> 102  53 ECONNABORTED -> 103   54 ECONNRESET -> 104
//  55 ENOBUFS -> 105    56 EISCONN -> 106        57 ENOTCONN -> 107
//  58 ESHUTDOWN -> 108  59 ETOOMANYREFS -> 109   60 ETIMEDOUT -> 110
//  61 ECONNREFUSED -> 111  62 ELOOP -> 40        63 ENAMETOOLONG -> 36
//  64 EHOSTDOWN -> 112  65 EHOSTUNREACH -> 113   66 ENOTEMPTY -> 39
//  67 EPROCLIM -> 11 (EAGAIN; Linux reports process limits as EAGAIN)
//  68 EUSERS -> 87      69 EDQUOT -> 122         70 ESTALE -> 116
//  71 EREMOTE -> 66     72..76 E*RPC*/EPROG* -> 5 (EIO; no Linux analog)
//  77 ENOLCK -> 37      78 ENOSYS -> 38          79 EFTYPE -> 22 (EINVAL)
//  80 EAUTH -> 13 (EACCES)  81 ENEEDAUTH -> 13   82 EPWROFF -> 5 (EIO)
//  83 EDEVERR -> 5      84 EOVERFLOW -> 75       85 EBADEXEC -> 8 (ENOEXEC)
//  86 EBADARCH -> 8     87 ESHLIBVERS -> 8       88 EBADMACHO -> 8
//  89 ECANCELED -> 125  90 EIDRM -> 43           91 ENOMSG -> 42
//  92 EILSEQ -> 84      93 ENOATTR -> 61 (ENODATA)  94 EBADMSG -> 74
//  95 EMULTIHOP -> 72   96 ENODATA -> 61         97 ENOLINK -> 67
//  98 ENOSR -> 63       99 ENOSTR -> 60          100 EPROTO -> 71
// 101 ETIME -> 62      102 EOPNOTSUPP -> 95      103 ENOPOLICY -> 22
// 104 ENOTRECOVERABLE -> 131  105 EOWNERDEAD -> 130  106 EQFULL -> 22
DATA runtime·cosmo_errno_xlat_tab+0(SB)/8, $0x0706050403020100
DATA runtime·cosmo_errno_xlat_tab+8(SB)/8, $0x0f0e0d0c230a0908
DATA runtime·cosmo_errno_xlat_tab+16(SB)/8, $0x1716151413121110
DATA runtime·cosmo_errno_xlat_tab+24(SB)/8, $0x1f1e1d1c1b1a1918
DATA runtime·cosmo_errno_xlat_tab+32(SB)/8, $0x595872730b222120
DATA runtime·cosmo_errno_xlat_tab+40(SB)/8, $0x61605f5e5d5c5b5a
DATA runtime·cosmo_errno_xlat_tab+48(SB)/8, $0x6968676665646362
DATA runtime·cosmo_errno_xlat_tab+56(SB)/8, $0x24286f6e6d6c6b6a
DATA runtime·cosmo_errno_xlat_tab+64(SB)/8, $0x42747a570b277170
DATA runtime·cosmo_errno_xlat_tab+72(SB)/8, $0x1626250505050505
DATA runtime·cosmo_errno_xlat_tab+80(SB)/8, $0x0808084b05050d0d
DATA runtime·cosmo_errno_xlat_tab+88(SB)/8, $0x484a3d542a2b7d08
DATA runtime·cosmo_errno_xlat_tab+96(SB)/8, $0x165f3e473c3f433d
DATA runtime·cosmo_errno_xlat_tab+104(SB)/8, $0x0000000000168283
GLOBL runtime·cosmo_errno_xlat_tab(SB), RODATA|NOPTR, $112
