// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gitauth uses 'git credential' to implement the GOAUTH protocol.
//
// See https://git-scm.com/docs/gitcredentials or run 'man gitcredentials' for
// information on how to configure 'git credential'.

package auth

import (
	"bytes"
	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/web/intercept"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitCredentialTimeout bounds 'git credential fill'. A helper that hangs or
// waits for a terminal must not hold up a build; the caller then falls back to
// git itself.
const gitCredentialTimeout = 30 * time.Second

// GitBasicAuth returns the credential git's own helpers hold for rawURL, as
// HTTP basic authentication. A caller that would otherwise run git over the
// network can present the same credential to a plain HTTPS request instead.
// dir is the working directory git reads its config in, and must be absolute,
// as GOAUTH=git requires. The helper runs with no terminal and no more than
// gitCredentialTimeout.
func GitBasicAuth(dir, rawURL string) (*url.Userinfo, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("git credential needs an absolute working directory, got %q", dir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCredentialTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "credential", "fill")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(fmt.Sprintf("url=%s\n", rawURL))
	// GIT_TERMINAL_PROMPT=0 and an empty GIT_ASKPASS make a helper that has no
	// credential fail rather than prompt on the terminal it does not have.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=")
	cmd.WaitDelay = gitCredentialTimeout
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("'git credential fill' failed (url=%s): %w", rawURL, err)
	}
	prefix, username, password := parseGitAuth(out)
	if prefix == "" || username == "" || password == "" {
		return nil, fmt.Errorf("'git credential fill' returned no credential for %s", rawURL)
	}
	if !strings.HasPrefix(rawURL, prefix) {
		return nil, fmt.Errorf("requested a credential for %s, got one for %s", rawURL, prefix)
	}
	return url.UserPassword(username, password), nil
}

const maxTries = 3

// runGitAuth retrieves credentials for the given url using
// 'git credential fill', validates them with a HEAD request
// (using the provided client) and updates the credential helper's cache.
// It returns the matching credential prefix, the http.Header with the
// Basic Authentication header set, or an error.
// The caller must not mutate the header.
func runGitAuth(client *http.Client, dir, url string) (string, http.Header, error) {
	if url == "" {
		// No explicit url was passed, but 'git credential'
		// provides no way to enumerate existing credentials.
		// Wait for a request for a specific url.
		return "", nil, fmt.Errorf("no explicit url was passed")
	}
	if dir == "" {
		// Prevent config-injection attacks by requiring an explicit working directory.
		// See https://golang.org/issue/29230 for details.
		panic("'git' invoked in an arbitrary directory") // this should be caught earlier.
	}
	cmd := exec.Command("git", "credential", "fill")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(fmt.Sprintf("url=%s\n", url))
	out, err := cmd.CombinedOutput()
	if err != nil {
		// out may hold the credential git printed before it failed, so it is
		// named only in the error, never echoed.
		return "", nil, fmt.Errorf("'git credential fill' failed (url=%s): %w", url, err)
	}
	parsedPrefix, username, password := parseGitAuth(out)
	if parsedPrefix == "" {
		return "", nil, fmt.Errorf("'git credential fill' failed for url=%s, could not parse url\n", url)
	}
	// Check that the URL Git gave us is a prefix of the one we requested.
	if !strings.HasPrefix(url, parsedPrefix) {
		return "", nil, fmt.Errorf("requested a credential for %s, but 'git credential fill' provided one for %s\n", url, parsedPrefix)
	}
	req, err := http.NewRequest("HEAD", parsedPrefix, nil)
	if err != nil {
		return "", nil, fmt.Errorf("internal error constructing HTTP HEAD request: %v\n", err)
	}
	req.SetBasicAuth(username, password)
	// Asynchronously validate the provided credentials using a HEAD request,
	// allowing the git credential helper to update its cache without blocking.
	// This avoids repeatedly prompting the user for valid credentials.
	// This is a best-effort update; the primary validation will still occur
	// with the caller's client.
	// The request is intercepted for testing purposes to simulate interactions
	// with the credential helper.
	intercept.Request(req)
	go updateGitCredentialHelper(client, req, out)

	// Return the parsed prefix and headers, even if credential validation fails.
	// The caller is responsible for the primary validation.
	return parsedPrefix, req.Header, nil
}

// parseGitAuth parses the output of 'git credential fill', extracting
// the URL prefix, user, and password.
// Any of these values may be empty if parsing fails.
func parseGitAuth(data []byte) (parsedPrefix, username, password string) {
	prefix := new(url.URL)
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "protocol":
			prefix.Scheme = value
		case "host":
			prefix.Host = value
		case "path":
			prefix.Path = value
		case "username":
			username = value
		case "password":
			password = value
		case "url":
			// Write to a local variable instead of updating prefix directly:
			// if the url field is malformed, we don't want to invalidate
			// information parsed from the protocol, host, and path fields.
			u, err := url.ParseRequestURI(value)
			if err != nil {
				if cfg.BuildX {
					log.Printf("malformed URL from 'git credential fill' (%v): %q\n", err, value)
					// Proceed anyway: we might be able to parse the prefix from other fields of the response.
				}
				continue
			}
			prefix = u
		}
	}
	return prefix.String(), username, password
}

// updateGitCredentialHelper validates the given credentials by sending a HEAD request
// and updates the git credential helper's cache accordingly. It retries the
// request up to maxTries times.
func updateGitCredentialHelper(client *http.Client, req *http.Request, credentialOutput []byte) {
	for range maxTries {
		release, err := base.AcquireNet()
		if err != nil {
			return
		}
		res, err := client.Do(req)
		if err != nil {
			release()
			continue
		}
		res.Body.Close()
		release()
		if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusUnauthorized {
			approveOrRejectCredential(credentialOutput, res.StatusCode == http.StatusOK)
			break
		}
	}
}

// approveOrRejectCredential approves or rejects the provided credential using
// 'git credential approve/reject'.
func approveOrRejectCredential(credentialOutput []byte, approve bool) {
	action := "reject"
	if approve {
		action = "approve"
	}
	cmd := exec.Command("git", "credential", action)
	cmd.Stdin = bytes.NewReader(credentialOutput)
	cmd.Run() // ignore error
}
