// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows && !darwin && !ios

package x509

// hasPlatformVerifier reports whether this build carries a systemVerify
// that asks the host to verify a chain. See platform_verifier.go.
const hasPlatformVerifier = false
