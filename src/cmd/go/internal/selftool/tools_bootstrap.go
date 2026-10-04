// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build compiler_bootstrap

package selftool

import (
	"cmd/asm"
	"cmd/cgo"
	"cmd/compile"
	"cmd/link"
)

// tools are the tools the bootstrap toolchain carries.
var tools = map[string]func([]string) int{
	"asm":     asm.Main,
	"cgo":     cgo.Main,
	"compile": compile.Main,
	"link":    link.Main,
}
