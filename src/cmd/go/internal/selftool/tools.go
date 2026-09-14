// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap && !compiler_bootstrap

package selftool

import (
	"cmd/asm"
	"cmd/cgo"
	"cmd/compile"
	"cmd/cover"
	"cmd/fix"
	"cmd/link"
	"cmd/preprofile"
	"cmd/vet"
)

// tools are the build tools the installed go command carries, by the name
// the go command asks for them under.
var tools = map[string]func([]string) int{
	"asm":        asm.Main,
	"cgo":        cgo.Main,
	"compile":    compile.Main,
	"cover":      cover.Main,
	"fix":        fix.Main,
	"link":       link.Main,
	"preprofile": preprofile.Main,
	"vet":        vet.Main,
}
