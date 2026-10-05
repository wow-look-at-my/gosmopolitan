// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package codehost

import (
	"net/url"

	"cmd/go/internal/auth"
)

// gitBasicAuth returns the credential git's own helpers hold for rawURL, so a
// private repository's ref advertisement can be read over HTTPS instead of a
// git ls-remote. dir is the working directory git reads its config in.
func gitBasicAuth(dir, rawURL string) (*url.Userinfo, error) {
	return auth.GitBasicAuth(dir, rawURL)
}
