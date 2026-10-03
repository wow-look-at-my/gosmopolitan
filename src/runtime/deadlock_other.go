// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !js

package runtime

// eventLoopCanWake reports whether the host environment's event loop can
// still deliver an event that wakes the program. Only meaningful on
// GOOS=js under GOWASM=threads (see event_js.go); this stub keeps the
// reference in checkdead compilable everywhere else.
func eventLoopCanWake() bool { return false }

// deadlockOSHint prints platform-specific context just before checkdead's
// "all goroutines are asleep" fatal error. It is only ever called on GOOS=js
// (see lock_js.go); this stub keeps the reference in checkdead compilable
// everywhere else.
func deadlockOSHint() {}
