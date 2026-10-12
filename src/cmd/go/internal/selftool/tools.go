// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap && !compiler_bootstrap

package selftool

import (
	"cmd/addr2line"
	"cmd/asm"
	"cmd/buildid"
	"cmd/cgo"
	"cmd/compile"
	"cmd/covdata"
	"cmd/cover"
	"cmd/embedstd"
	"cmd/fix"
	"cmd/link"
	"cmd/nm"
	"cmd/objdump"
	"cmd/pack"
	"cmd/pprof"
	"cmd/preprofile"
	"cmd/test2json"
	"cmd/trace"
	"cmd/vet"
)

// tools are every tool the installed go command carries, by the name the
// go command asks for them under. Keep in sync with linkedTools in
// cmd/dist/build.go.
var tools = map[string]func([]string) int{
	"addr2line":  addr2line.Main,
	"asm":        asm.Main,
	"buildid":    buildid.Main,
	"cgo":        cgo.Main,
	"compile":    compile.Main,
	"covdata":    covdata.Main,
	"cover":      cover.Main,
	"embedstd":   embedstd.Main,
	"fix":        fix.Main,
	"link":       link.Main,
	"nm":         nm.Main,
	"objdump":    objdump.Main,
	"pack":       pack.Main,
	"pprof":      pprof.Main,
	"preprofile": preprofile.Main,
	"test2json":  test2json.Main,
	"trace":      trace.Main,
	"vet":        vet.Main,
}
