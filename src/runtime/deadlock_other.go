// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !js

package runtime

// deadlockOSHint prints platform-specific context just before checkdead's
// "all goroutines are asleep" fatal error. It is only ever called on GOOS=js
// (see lock_js.go); this stub keeps the reference in checkdead compilable
// everywhere else.
func deadlockOSHint() {}
