// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm && !wasm.threads

#include "textflag.h"

// Without GOWASM=threads there is only one thread, so there is nothing to
// yield to; this must never be called.
TEXT runtime·osyield(SB), NOSPLIT, $0-0
	UNDEF
