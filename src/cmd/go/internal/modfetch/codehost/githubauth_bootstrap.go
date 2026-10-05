// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

package codehost

import "net/url"

// githubBasicAuth is unavailable in the bootstrap go command, which fetches no
// private module over the network. The request goes out anonymously and
// loadRefs falls back to git ls-remote as it always has.
func githubBasicAuth(rawURL string) *url.Userinfo {
	return nil
}
