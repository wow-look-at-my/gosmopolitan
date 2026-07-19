// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !js

package runtime

// wasmIdleMarkCanYield is a stub for non-js platforms; idle mark
// throttling (see wasmIdleMarkThrottled in mgcmark.go) is js/wasm only.
func wasmIdleMarkCanYield() bool {
	return false
}
