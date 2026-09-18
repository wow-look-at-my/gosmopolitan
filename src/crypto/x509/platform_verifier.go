// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows || darwin || ios

package x509

// hasPlatformVerifier reports whether this build carries a systemVerify
// that asks the host to verify a chain. It is a BUILD property, not a
// host one: a cosmo binary running on macOS has no Security.framework
// binding compiled in, and its systemVerify answers (nil, nil) - which
// Verify would read as "the platform accepted this certificate".
const hasPlatformVerifier = true
