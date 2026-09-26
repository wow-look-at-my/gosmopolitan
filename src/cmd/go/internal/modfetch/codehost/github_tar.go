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

// parseTarGzArchive reads a git archive tar.gz. Its pax global header
// names the commit, and every entry carries the commit time.
func parseTarGzArchive(src io.Reader, hash string) ([]archiveEntry, time.Time, string, error) {
	unzipped, err := gzip.NewReader(src)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	defer unzipped.Close()
	reader := tar.NewReader(unzipped)
	builder := newArchiveBuilder()
	embedded := ""
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, time.Time{}, "", err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			embedded = header.PAXRecords["comment"]
			continue
		}
		var content io.Reader = reader
		mode := header.FileInfo().Mode()
		if header.Typeflag == tar.TypeSymlink {
			content = strings.NewReader(header.Linkname)
		}
		if err := builder.add(header.Name, mode, header.ModTime, content); err != nil {
			return nil, time.Time{}, "", err
		}
	}
	return builder.finish(embedded, hash)
}
