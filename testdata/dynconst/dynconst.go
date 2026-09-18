// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command dynconst proves that runtime.GOOS and runtime.GOARCH serve
// both readings this port needs. They are variables, so a plain read
// names the HOST the APE booted on. A constant declaration folds them to
// the build value instead, which is what code in the wild asks for when
// it writes `const unaligned = runtime.GOARCH == "386" || ...`: the
// payload's architecture decides how the compiled code behaves.
package main

import (
	"fmt"
	"runtime"
)

// The shape golang.org/x/crypto/chacha20 uses. It must compile.
const unaligned = runtime.GOARCH == "386" ||
	runtime.GOARCH == "amd64" ||
	runtime.GOARCH == "arm64" ||
	runtime.GOARCH == "ppc64le" ||
	runtime.GOARCH == "s390x"

const (
	constArch = runtime.GOARCH
	constOS   = runtime.GOOS
)

func main() {
	fmt.Printf("const %s/%s\n", constOS, constArch)
	fmt.Printf("read %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("unaligned %v\n", unaligned)
}
