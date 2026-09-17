// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (unix && !cosmo) || (js && wasm) || wasip1

package poll

// runtime_cancelIO has nothing to end: every descriptor a close can race
// here is either in the poller, which evict wakes, or blocking by the
// caller's own choice.
func runtime_cancelIO(fd uintptr) {}
