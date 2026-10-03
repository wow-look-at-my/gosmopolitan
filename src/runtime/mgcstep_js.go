// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build js && wasm

package runtime

// wasm_gc_mark_step implements the "go_gc_mark_step" wasm export, the
// JS-visible contract being.
//
//go:wasmexport go_gc_mark_step
func wasm_gc_mark_step(budgetMs float64) bool {
	return gcMarkStep(budgetMs)
}
