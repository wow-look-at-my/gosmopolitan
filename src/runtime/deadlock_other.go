// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !js

package runtime

// eventLoopCanWake reports whether the host environment's event loop can still deliver an event that wakes the program.
func eventLoopCanWake() bool { return false }

// deadlockOSHint prints platform-specific context before checkdead's "all goroutines are asleep" fatal error.
func deadlockOSHint() {}
