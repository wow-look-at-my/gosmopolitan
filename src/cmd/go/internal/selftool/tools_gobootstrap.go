// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap && !compiler_bootstrap

package selftool

// go_bootstrap carries no tools: it drives the ones cmd/dist installed under
// pkg/tool, the way the go command always has.
var tools = map[string]func([]string) int{}
