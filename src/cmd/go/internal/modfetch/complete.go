// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modfetch

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cache"
	"cmd/go/internal/gendep"
	"cmd/go/internal/lockedfile"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

// A module version is fetched from the proxy once. What the build reads is
// the complete module: the proxy's zip plus every file the module's own
// generators add to it. The complete zip is one entry in the build cache,
// under the module, its version and the go.sum checksum of the proxy's zip,
// so a build that has the checksum takes the complete zip from the cache
// server and never asks the proxy. The proxy is asked on a miss, its zip is
// verified against go.sum as ever, the module is completed here, and the
// result is stored for every build after this one.
//
// The entry is one blob: a header line naming the proxy zip's checksum, which
// go.sum verifies, and the complete zip's own checksum, which `go mod verify`
// holds the extracted directory to; then the complete zip.

// completeVersion names the entry format. A change to what the entry holds, or
// to how a module is completed, is a new version, and every module is
// completed again under it.
const completeVersion = "gozip v1"

// completeKey is the cache key of the complete zip of mod whose proxy zip has
// checksum sum. An org module has no checksum: its version names its commit.
func completeKey(mod module.Version, sum string) cache.ActionID {
	h := cache.NewHash("modzip")
	fmt.Fprintf(h, "%s\n%s\n%s\n%s\n", completeVersion, mod.Path, mod.Version, sum)
	return h.Sum()
}

// completeEntry is what the cache holds for a module version.
type completeEntry struct {
	proxySum string // go.sum checksum of the proxy's zip
	fullSum  string // checksum of the complete zip
	zip      []byte
}

func (e *completeEntry) encode() []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s %s %s\n", completeVersion, e.proxySum, e.fullSum)
	buf.Write(e.zip)
	return buf.Bytes()
}

func decodeComplete(body []byte) (*completeEntry, error) {
	nl := bytes.IndexByte(body, '\n')
	if nl < 0 {
		return nil, fmt.Errorf("complete module entry has no header")
	}
	fields := strings.Fields(string(body[:nl]))
	if len(fields) != 4 || fields[0]+" "+fields[1] != completeVersion || !isValidSum([]byte(fields[2])) || !isValidSum([]byte(fields[3])) {
		return nil, fmt.Errorf("complete module entry has a malformed header %q", body[:nl])
	}
	return &completeEntry{proxySum: fields[2], fullSum: fields[3], zip: body[nl+1:]}, nil
}

// completeMode reads GOGENERATEDEPS: "off" reads exactly what the proxy
// serves, "verify" completes every module here and holds the result to the
// cache's copy, and anything else takes the cache's copy when there is one.
func completeMode() string {
	return os.Getenv("GOGENERATEDEPS")
}

// cachedComplete answers the complete zip of mod from the cache, when the
// build has a checksum to ask under and the cache holds one.
func (f *Fetcher) cachedComplete(mod module.Version) (*completeEntry, bool) {
	switch completeMode() {
	case "off", "verify":
		return nil, false
	}
	sum, ok := f.completeSum(mod)
	if !ok {
		return nil, false
	}
	body, _, err := cache.GetBytes(cache.Default(), completeKey(mod, sum))
	if err != nil {
		return nil, false
	}
	entry, err := decodeComplete(body)
	if err != nil {
		base.Fatalf("go: %s@%s: %v", mod.Path, mod.Version, err)
	}
	return entry, true
}

// completeSum answers the checksum the complete zip is keyed under: the go.sum
// entry for the proxy's zip, or "" for an org module, whose version is its
// commit. A module go.sum does not list yet has no key: the proxy answers
// first, and the checksum it yields is recorded, as ever.
func (f *Fetcher) completeSum(mod module.Version) (string, bool) {
	if !HaveSum(f, mod) {
		return "", false
	}
	sum, ok := f.RecordedSum(mod)
	if !ok {
		// An org module: no sum at all.
		return "", HaveSum(f, mod)
	}
	return sum, true
}

// completeModule completes the module extracted at dir, from the proxy zip
// whose checksum proxySum go.sum verified, packs the complete tree, records
// its checksum beside the module, and stores the complete zip in the cache.
// It answers the complete zip's checksum.
//
// A generator that answers differently on two machines gives the cache two
// bodies for one key, and every build after the second reads whichever won.
// So the store first asks the cache: an entry already there holds the same
// bytes, or the build stops and names the file that differs.
func (f *Fetcher) completeModule(ctx context.Context, mod module.Version, dir, proxySum string) (string, error) {
	if completeMode() != "off" {
		if _, err := gendep.Complete(dir, mod.Path); err != nil {
			return "", fmt.Errorf("generating %s@%s: %w", mod.Path, mod.Version, err)
		}
	}
	var buf bytes.Buffer
	if err := modzip.CreateFromDir(&buf, mod, dir); err != nil {
		return "", err
	}
	fullSum, err := zipSum(buf.Bytes())
	if err != nil {
		return "", err
	}
	if err := writeSumFile(ctx, mod, "complete", fullSum); err != nil {
		return "", err
	}
	if completeMode() == "off" {
		return fullSum, nil
	}
	sum, ok := f.completeSum(mod)
	if !ok {
		sum = proxySum
	}
	entry := &completeEntry{proxySum: proxySum, fullSum: fullSum, zip: buf.Bytes()}
	key := completeKey(mod, sum)
	if body, _, err := cache.GetBytes(cache.Default(), key); err == nil {
		have, err := decodeComplete(body)
		if err != nil {
			return "", fmt.Errorf("%s@%s: %v", mod.Path, mod.Version, err)
		}
		if have.fullSum != fullSum {
			base.Fatalf("go: %s@%s completed here differs from the cache's copy: %s", mod.Path, mod.Version, zipDifference(have.zip, entry.zip))
		}
		return fullSum, nil
	}
	if err := cache.PutBytes(cache.Default(), key, entry.encode()); err != nil {
		return "", err
	}
	return fullSum, nil
}

// zipSum answers the h1 checksum of a zip held in memory.
func zipSum(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	files := make([]string, 0, len(r.File))
	byName := make(map[string]*zip.File, len(r.File))
	for _, file := range r.File {
		files = append(files, file.Name)
		byName[file.Name] = file
	}
	return dirhash.Hash1(files, func(name string) (io.ReadCloser, error) {
		return byName[name].Open()
	})
}

// zipDifference names the first file that differs between two zips: one
// present in only one of them, or one with different bytes.
func zipDifference(left, right []byte) string {
	lr, err := zip.NewReader(bytes.NewReader(left), int64(len(left)))
	if err != nil {
		return err.Error()
	}
	rr, err := zip.NewReader(bytes.NewReader(right), int64(len(right)))
	if err != nil {
		return err.Error()
	}
	rightFiles := map[string]*zip.File{}
	for _, file := range rr.File {
		rightFiles[file.Name] = file
	}
	for _, file := range lr.File {
		other, ok := rightFiles[file.Name]
		if !ok {
			return "the cache's copy carries " + file.Name + " and this one does not"
		}
		if file.CRC32 != other.CRC32 || file.UncompressedSize64 != other.UncompressedSize64 {
			return file.Name + " differs"
		}
		delete(rightFiles, file.Name)
	}
	for name := range rightFiles {
		return "this copy carries " + name + " and the cache's does not"
	}
	return "no file differs, and the checksums do"
}

// writeSumFile records sum in the module's download cache under suffix.
func writeSumFile(ctx context.Context, mod module.Version, suffix, sum string) error {
	path, err := CachePath(ctx, mod, suffix)
	if err != nil {
		return err
	}
	return lockedfile.Write(path, strings.NewReader(sum), 0o666)
}

// CompleteSum answers the checksum of the complete module extracted for mod,
// which is the checksum of its directory, or "" when none is recorded.
func CompleteSum(ctx context.Context, mod module.Version) string {
	path, err := CachePath(ctx, mod, "complete")
	if err != nil {
		return ""
	}
	data, err := lockedfile.Read(path)
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(data))
}
