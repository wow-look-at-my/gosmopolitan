// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/filepathlite"
	"internal/testlog"
	"sync"
)

// withTestUnit hands a child copy of a test binary holding several packages'
// tests the package this process runs, when the caller replaced the child's
// environment. A test that starts its own binary again, or hands it to
// something that starts it, such as a CGI handler, names only a test of its
// own. The binary needs the package too, and finds it in the environment,
// which testing/internal/testdeps set for this process.
//
// Only a start of this same executable gets it this way, marked so that the
// child takes the added entries out again before its tests see them.
func withTestUnit(name string, attr *ProcAttr) *ProcAttr {
	unit := testlog.Unit()
	if unit == "" {
		return attr
	}
	var env []string
	dir := ""
	if attr != nil {
		env = attr.Env
		dir = attr.Dir
	}
	if !startsThisExecutable(name, dir) {
		return attr
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

// thisExecutable is the running executable's file, read once.
var thisExecutable = sync.OnceValue(func() FileInfo {
	path, err := Executable()
	if err != nil {
		return nil
	}
	info, err := statNolog(path)
	if err != nil {
		return nil
	}
	return info
})

// startsThisExecutable reports whether starting name, relative to dir when it
// is relative, starts the running executable. Neither stat is reported to the
// test log: the caller's own look at name already is.
func startsThisExecutable(name, dir string) bool {
	self := thisExecutable()
	if self == nil {
		return false
	}
	if dir != "" && !filepathlite.IsAbs(name) {
		name = dir + string(PathSeparator) + name
	}
	info, err := statNolog(name)
	if err != nil {
		return false
	}
	return SameFile(self, info)
}
