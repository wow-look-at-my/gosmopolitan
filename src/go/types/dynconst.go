// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package types

import (
	"go/constant"
	"os"
	"runtime"
)

// runtime.GOOS and runtime.GOARCH are VARIABLES on the cosmo port: one APE
// boots on kernels, and a payload can run on a machine of another
// architecture, so both are read at startup.
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
