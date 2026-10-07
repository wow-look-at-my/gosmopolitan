// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

package test

// pemRootCerts fails: go_bootstrap carries no crypto/x509, which is a cgo
// package, and builds the toolchain without caching a test result.
func pemRootCerts(data []byte) []rootCert {
	panic("go_bootstrap cannot parse root certificates")
}
