// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo || !arm64

package main

// AT_HWCAP is an arm64 question; an x86 program asks the CPU itself.
func checkHWCAP() { ok("hwcap", "not arm64") }
