// All rights reserved. Use of this source code is governed by a
// BSD-style license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package codehost

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"strings"
	"time"
)

// githubTarToArchive converts a git archive tar.gz. Every entry carries the
// commit time.
func githubTarToArchive(src io.Reader, hash string) ([]byte, time.Time, error) {
	unzipped, err := gzip.NewReader(src)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer unzipped.Close()
	reader := tar.NewReader(unzipped)
	builder := newArchiveBuilder(hash)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, time.Time{}, err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
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
