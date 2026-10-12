// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// secureMode is declared in os_cosmo.go and initialized from AT_SECURE in sysauxv.

func initSecureMode() {
}

func isSecureMode() bool {
	return secureMode
}
