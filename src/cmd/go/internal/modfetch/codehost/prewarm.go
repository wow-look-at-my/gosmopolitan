// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codehost

import "sync"

// Prewarm starts the git lookups that the first GitHub fetch needs, one time
// per process. They then run beside each other, not one after another.
var Prewarm = sync.OnceFunc(func() {
	go gitInsteadOf()
	go githubBasicAuth("https://github.com/")
})
