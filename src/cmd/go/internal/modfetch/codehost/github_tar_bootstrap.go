// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

package codehost

import (
	"errors"
	"io"
	"time"
)

// githubTarToArchive is not in go_bootstrap: archive/tar imports os/user,
// which needs cgo. go_bootstrap has no HTTP client, so it never downloads.
func githubTarToArchive(src io.Reader, hash string) ([]byte, time.Time, error) {
	return nil, time.Time{}, errors.New("no tar in bootstrap go command")
}
