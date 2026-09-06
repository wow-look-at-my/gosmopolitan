// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"go/constant"
	"os"
	"runtime"
)

// runtime.GOOS and runtime.GOARCH are VARIABLES on the cosmo port: one
// APE boots on three kernels, and a payload can run on a machine of
// another architecture, so both are read at startup. Code in the wild
// still writes `const unaligned = runtime.GOARCH == "386" || ...`, and a
// constant context is exactly where the build's own answer is the right
// one - the payload's architecture decides how the compiled code
// behaves, whatever machine runs it.
//
// So in a context that REQUIRES a constant, a reference to one of them
// folds to the build value. Every other reference loads the variable and
// reports the host. Recognition is by package path and name because the
// export data carries no marker for this. The mirror of this file is
// cmd/compile/internal/types2/dynconst.go, which reads the target from
// buildcfg; a tool linking go/types reads the environment cmd/go set for
// it, and falls back to its own platform.
func dynamicConstVal(obj *Var) (constant.Value, bool) {
	if obj.pkg == nil || obj.pkg.Path() != "runtime" || obj.parent != obj.pkg.scope {
		return nil, false
	}
	switch obj.name {
	case "GOOS":
		return constant.MakeString(envOr("GOOS", runtime.GOOS)), true
	case "GOARCH":
		return constant.MakeString(envOr("GOARCH", runtime.GOARCH)), true
	}
	return nil, false
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
