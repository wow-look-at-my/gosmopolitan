// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import "internal/testlog"

// withTestUnit hands a child the package whose tests this process runs, when
// this is a test binary holding several packages' tests and the caller
// replaced the child's environment. A test that starts its own binary again,
// or hands it to something that starts it, such as a CGI handler or a program
// of its own that passes its environment on, names only a test of its own.
// The binary needs the package too, and finds it in the environment, which
// testing/internal/testdeps set for this process.
//
// The added entries are marked, so that the copy of the test binary that
// reads them takes them out again before its tests see them.
func withTestUnit(attr *ProcAttr) *ProcAttr {
	unit := testlog.Unit()
	if unit == "" {
		return attr
	}
	var env []string
	if attr != nil {
		env = attr.Env
	}
	if env == nil {
		env = Environ()
	}
	for _, kv := range env {
		if len(kv) > len(testlog.UnitEnv) && kv[:len(testlog.UnitEnv)+1] == testlog.UnitEnv+"=" {
			return attr
		}
	}
	child := ProcAttr{}
	if attr != nil {
		child = *attr
	}
	child.Env = append(env[:len(env):len(env)], testlog.UnitEnv+"="+unit, testlog.UnitImplicitEnv+"=1")
	return &child
}
