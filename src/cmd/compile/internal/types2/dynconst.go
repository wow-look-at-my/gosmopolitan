// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package types2

import (
	"go/constant"
	"internal/buildcfg"
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
		return constant.MakeString(buildcfg.GOOS), true
	case "GOARCH":
		return constant.MakeString(buildcfg.GOARCH), true
	}
	return nil, false
}
