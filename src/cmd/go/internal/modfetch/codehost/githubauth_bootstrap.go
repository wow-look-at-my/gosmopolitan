// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

package codehost

import (
	"errors"
	"net/url"
)

// gitBasicAuth is unavailable in the bootstrap go command, which fetches no
// private module over the network. The ref advertisement then fails and
// loadRefs falls back to git ls-remote as it always has.
func gitBasicAuth(dir, rawURL string) (*url.Userinfo, error) {
	return nil, errors.New("the bootstrap go command reads no git credential")
}
