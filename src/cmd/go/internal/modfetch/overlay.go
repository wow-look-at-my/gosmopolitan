// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modfetch

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cache"
	"cmd/go/internal/gendep"
	"cmd/go/internal/lockedfile"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

// A module is fetched in two parts. The BASE is the zip the proxy serves, or
// what the repository holds, verified against go.sum exactly as ever. The
// OVERLAY is the files the module's own generators add to it, and it is the one
// part go-s3-server caches: the proxy and go.sum already serve and pin the base.
//
// An empty overlay is stored too. It is what says the module needs nothing, and
// it is what lets the next build skip generation entirely.

// overlayVersion names the entry format. A change to what the entry holds, or
// to how a module is completed, is a new version, and every module is completed
// again under it. A build that completes a module differently but reads the
// entries an older one stored serves that older answer to the whole fleet.
const overlayVersion = "overlay v4"

// overlayKey is the cache key of the overlay of mod whose base zip has checksum
// baseSum. An org module has no checksum: its pseudo-version names its commit.
func overlayKey(mod module.Version, baseSum string) cache.ActionID {
	h := cache.NewHash("modzip")
	fmt.Fprintf(h, "%s\n%s\n%s\n%s\n", overlayVersion, mod.Path, mod.Version, baseSum)
	return h.Sum()
}

// overlayEntry is what the cache holds for a module version: the checksum of
// the added files, and a zip of them in the module's own <path>@<version>/
// layout.
type overlayEntry struct {
	sum string
	zip []byte
}

func (entry *overlayEntry) encode() []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s %s\n", overlayVersion, entry.sum)
	buf.Write(entry.zip)
	return buf.Bytes()
}

func decodeOverlay(body []byte) (*overlayEntry, error) {
	nl := bytes.IndexByte(body, '\n')
	if nl < 0 {
		return nil, fmt.Errorf("module overlay entry has no header")
	}
	fields := strings.Fields(string(body[:nl]))
	if len(fields) != 3 || fields[0]+" "+fields[1] != overlayVersion || !isValidSum([]byte(fields[2])) {
		return nil, fmt.Errorf("module overlay entry has a malformed header %q", body[:nl])
	}
	return &overlayEntry{sum: fields[2], zip: body[nl+1:]}, nil
}

// InstallTargets are the package paths of a `go install pkg@version`, set
// before anything is fetched. See completingSelf.
var InstallTargets []string

// completingSelf reports whether mod provides a package this command is
// installing. Completing such a module cannot terminate: completing
// golang.org/x/tools runs the stringer directive it carries, and stringer is
// the program being installed from it. The generator lives in the module that
// needs it, so there is no order in which the module is ready first.
func completingSelf(mod module.Version) bool {
	for _, target := range InstallTargets {
		if target == mod.Path || strings.HasPrefix(target, mod.Path+"/") {
			return true
		}
	}
	return false
}

// completeDir completes the module extracted at dir: it adds the files the
// module's own generators write, from the cache when the cache holds them and
// by running the generators when it does not.
func (f *Fetcher) completeDir(ctx context.Context, mod module.Version, dir string) error {
	// A module that carries no directive completes to itself. Asking this first
	// keeps the build cache out of the fetch of every such module, and `go mod
	// download` needs no build cache to fetch one.
	pkgs := gendep.Packages(dir)
	if len(pkgs) == 0 {
		return nil
	}
	// The one module that cannot complete. Said out loud, because the package
	// installed from it is built from the zip alone: whatever its own
	// generators would have added is missing.
	if completingSelf(mod) {
		fmt.Fprintf(os.Stderr, "go: %s@%s provides the program being installed, so its own generators do not run\n", mod.Path, mod.Version)
		return nil
	}
	key := overlayKey(mod, recordedZipHash(ctx, mod))
	if body, _, err := cache.GetBytes(cache.Default(), key); err == nil {
		entry, err := decodeOverlay(body)
		if err != nil {
			base.Fatalf("go: %s@%s: %v", mod.Path, mod.Version, err)
		}
		if err := applyOverlay(mod, dir, entry.zip); err != nil {
			return err
		}
		overlayDebugf("modfetch: %s@%s: took %d bytes of overlay from the cache", mod.Path, mod.Version, len(entry.zip))
		return recordComplete(ctx, mod, dir)
	}

	added, partial, err := gendep.Complete(dir, mod.Path, pkgs)
	if err != nil {
		return fmt.Errorf("generating %s@%s: %w", mod.Path, mod.Version, err)
	}
	// A partial answer is a fact about this machine's installed programs. The
	// key names neither, so storing one would serve it to every machine that
	// asks, including the machines that can produce the whole answer.
	if partial {
		overlayDebugf("modfetch: %s@%s: completed without a program this host lacks, so nothing is stored", mod.Path, mod.Version)
		return recordComplete(ctx, mod, dir)
	}
	overlay, err := packOverlay(mod, dir, added)
	if err != nil {
		return err
	}
	sum, err := zipSum(overlay)
	if err != nil {
		return err
	}
	// A generator that answers differently on two machines would give the cache
	// two bodies for one key, and every build after the second would read
	// whichever won. So the store asks first: an entry already there holds the
	// same files, or the build stops and names the file that differs.
	if body, _, err := cache.GetBytes(cache.Default(), key); err == nil {
		have, err := decodeOverlay(body)
		if err != nil {
			return fmt.Errorf("%s@%s: %w", mod.Path, mod.Version, err)
		}
		if have.sum != sum {
			base.Fatalf("go: %s@%s: what its generators added here differs from the cache's copy: %s", mod.Path, mod.Version, zipDifference(have.zip, overlay))
		}
	} else if err := cache.PutBytes(cache.Default(), key, (&overlayEntry{sum: sum, zip: overlay}).encode()); err != nil {
		return err
	}
	overlayDebugf("modfetch: %s@%s: stored %d added files as %d bytes of overlay", mod.Path, mod.Version, len(added), len(overlay))
	return recordComplete(ctx, mod, dir)
}

// packOverlay archives the added files of the module extracted at dir, each
// named as the module's own zip would name it.
func packOverlay(mod module.Version, dir string, added []string) ([]byte, error) {
	prefix := mod.Path + "@" + mod.Version + "/"
	var buf bytes.Buffer
	out := zip.NewWriter(&buf)
	for _, rel := range added {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		into, err := out.Create(prefix + filepath.ToSlash(rel))
		if err != nil {
			return nil, err
		}
		if _, err := into.Write(body); err != nil {
			return nil, err
		}
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// applyOverlay writes the files an overlay carries into the module extracted at
// dir. A name outside the module's own prefix is refused: the entry decides
// what it writes, and the cache is shared.
func applyOverlay(mod module.Version, dir string, overlay []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(overlay), int64(len(overlay)))
	if err != nil {
		return err
	}
	prefix := mod.Path + "@" + mod.Version + "/"
	for _, file := range reader.File {
		rel, ok := strings.CutPrefix(file.Name, prefix)
		if !ok || rel == "" {
			return fmt.Errorf("%s@%s: overlay names %q outside the module", mod.Path, mod.Version, file.Name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if !strings.HasPrefix(dst, dir+string(filepath.Separator)) {
			return fmt.Errorf("%s@%s: overlay names %q outside the module", mod.Path, mod.Version, file.Name)
		}
		from, err := file.Open()
		if err != nil {
			return err
		}
		err = writeOverlayFile(dst, from)
		from.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeOverlayFile(dst string, body io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
		return err
	}
	into, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(into, body); err != nil {
		into.Close()
		return err
	}
	return into.Close()
}

// recordComplete records the checksum of the completed module directory, which
// `go mod verify` holds that directory to. It covers the base files and the
// overlay's alike: every checksum here names everything it covers.
func recordComplete(ctx context.Context, mod module.Version, dir string) error {
	sum, err := dirhash.HashDir(dir, mod.Path+"@"+mod.Version, dirhash.DefaultHash)
	if err != nil {
		return err
	}
	path, err := CachePath(ctx, mod, "complete")
	if err != nil {
		return err
	}
	return lockedfile.Write(path, strings.NewReader(sum), 0o666)
}

// CompleteSum answers the checksum recorded for mod's completed directory, or
// "" when none is recorded.
func CompleteSum(ctx context.Context, mod module.Version) string {
	path, err := CachePath(ctx, mod, "complete")
	if err != nil {
		return ""
	}
	data, err := lockedfile.Read(path)
	if err != nil {
		return ""
	}
	sum := string(bytes.TrimSpace(data))
	if !isValidSum([]byte(sum)) {
		return ""
	}
	return sum
}

// recordedZipHash answers the checksum DownloadZip recorded for mod's base zip,
// which go.sum verified. A module fetched straight from its repository has
// none, and its pseudo-version names the commit instead.
func recordedZipHash(ctx context.Context, mod module.Version) string {
	path, err := CachePath(ctx, mod, "ziphash")
	if err != nil {
		return ""
	}
	data, err := lockedfile.Read(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			base.Fatalf("go: %s@%s: %v", mod.Path, mod.Version, err)
		}
		return ""
	}
	sum := string(bytes.TrimSpace(data))
	if !isValidSum([]byte(sum)) {
		return ""
	}
	return sum
}

// zipSum answers the h1 checksum of a zip held in memory.
func zipSum(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(reader.File))
	byName := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
		byName[file.Name] = file
	}
	return dirhash.Hash1(names, func(name string) (io.ReadCloser, error) {
		return byName[name].Open()
	})
}

// zipDifference names the first file that differs between two zips: one present
// in only one of them, or one whose bytes differ.
func zipDifference(left, right []byte) string {
	leftZip, err := zip.NewReader(bytes.NewReader(left), int64(len(left)))
	if err != nil {
		return err.Error()
	}
	rightZip, err := zip.NewReader(bytes.NewReader(right), int64(len(right)))
	if err != nil {
		return err.Error()
	}
	rightFiles := map[string]*zip.File{}
	for _, file := range rightZip.File {
		rightFiles[file.Name] = file
	}
	for _, file := range leftZip.File {
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

// overlayDebugf reports what the cache did for a module's overlay, under the
// same variable that makes the build cache report itself.
func overlayDebugf(format string, args ...any) {
	if os.Getenv("GOCACHEDEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "go: "+format+"\n", args...)
}
