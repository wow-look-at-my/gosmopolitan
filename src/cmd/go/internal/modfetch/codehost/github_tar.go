// All rights reserved. Use of this source code is governed by a
// BSD-style license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package codehost

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"time"
)

// githubTarToArchive converts a git archive tar.gz. Its pax global header
// names the commit, and every entry carries the commit time.
func githubTarToArchive(src io.Reader, hash string) ([]byte, time.Time, error) {
	unzipped, err := gzip.NewReader(src)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer unzipped.Close()
	reader := tar.NewReader(unzipped)
	builder := newArchiveBuilder(hash)
	sawCommit := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, time.Time{}, err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			if id := header.PAXRecords["comment"]; id != hash {
				return nil, time.Time{}, fmt.Errorf("archive is of commit %q, want %s", id, hash)
			}
			sawCommit = true
			continue
		}
		if !sawCommit {
			return nil, time.Time{}, fmt.Errorf("archive names no commit")
		}
		var content io.Reader = reader
		mode := header.FileInfo().Mode()
		if header.Typeflag == tar.TypeSymlink {
			content = strings.NewReader(header.Linkname)
		}
		if err := builder.add(header.Name, mode, header.ModTime, content); err != nil {
			return nil, time.Time{}, err
		}
	}
	return builder.finish()
}
