// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

TEXT runtime·exitThread(SB), NOSPLIT, $0-0
	UNDEF

TEXT runtime·osyield(SB), NOSPLIT, $0-0
	UNDEF

TEXT runtime·currentMemory(SB), NOSPLIT, $0
	Get SP
	CurrentMemory
	I32Store ret+0(FP)
	RET

TEXT runtime·growMemory(SB), NOSPLIT, $0
	Get SP
	I32Load pages+0(FP)
	GrowMemory
	I32Store ret+8(FP)
	RET
