// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package exec

import "os"

func startProcess(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
	return os.StartProcess(name, argv, attr)
}
