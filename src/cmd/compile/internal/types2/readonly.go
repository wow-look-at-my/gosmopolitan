// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types2

import "cmd/compile/internal/syntax"

// readonlyVar answers the readonly var an expression names, and nil when it
// names anything else. A readonly var reads as a value from another package,
// so this is what tells that failure apart from any other unassignable one.
// Depth: docs/READONLY-VARS.md.
func (check *Checker) readonlyVar(e syntax.Expr) *Var {
	var obj Object
	switch e := syntax.Unparen(e).(type) {
	case *syntax.Name:
		obj = check.lookup(e.Value)
	case *syntax.SelectorExpr:
		if base, _ := e.X.(*syntax.Name); base != nil {
			if pn, _ := check.lookup(base.Value).(*PkgName); pn != nil {
				obj = pn.imported.scope.Lookup(e.Sel.Value)
			}
		}
	}
	if v, _ := obj.(*Var); v != nil && v.readonly {
		return v
	}
	return nil
}
