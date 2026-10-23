//go:build gc

#include "textflag.h"

TEXT ·Syscall(SB),NOSPLIT,$0-56
	B	syscall·Syscall(SB)

TEXT ·Syscall6(SB),NOSPLIT,$0-80
	B	syscall·Syscall6(SB)

TEXT ·RawSyscall(SB),NOSPLIT,$0-56
	B	syscall·RawSyscall(SB)
