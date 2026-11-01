// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo && !darwin

package main

import "errors"

// darwinProcArgs has no caller here: cosmoHostOS names this build's GOOS,
// and that is not darwin.
func darwinProcArgs() ([]byte, error) {
	return nil, errors.New("no darwin sysctl in this build")
}
