// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package codehost

import (
	"net/url"
	"os"
	"sync"

	"cmd/go/internal/auth"
)

// githubBasicAuth returns the credential git's own helpers hold for a
// github.com URL, resolved once per process. Every origin of one build resolves
// through it, so the helper runs once rather than once per request, and a URL
// only decides which host git matches. With no credential the answer is nil,
// and the request goes out anonymously.
func githubBasicAuth(rawURL string) *url.Userinfo {
	githubCredential.Do(func() {
		dir, err := os.Getwd()
		if err != nil {
			return
		}
		githubCredentialValue, _ = auth.GitBasicAuth(dir, rawURL)
	})
	return githubCredentialValue
}

// resetGitHubCredential forgets the cached credential, so a test judges one
// helper run rather than whatever an earlier test resolved.
func resetGitHubCredential() {
	githubCredential = sync.Once{}
	githubCredentialValue = nil
}

var (
	githubCredential      sync.Once
	githubCredentialValue *url.Userinfo
)
