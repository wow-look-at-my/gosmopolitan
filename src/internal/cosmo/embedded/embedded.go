// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package embedded reads the standard library a go binary carries inside
// itself: a blob of compiled package archives, the assembly headers and a
// manifest per target, appended past the APE's load span by the linker's
// -apeappend flag and found through a trailer at the end of the file.
//
// A tool names an entry as "self:<name>", for example
// "self:std/cosmo_amd64/fmt.a" in an importcfg or "self:include" as an
// assembler include directory, and resolves it here with no file of its own.
package embedded

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Prefix marks a name that resolves inside this binary.
const Prefix = "self:"

// blobMagic opens a blob; trailerMagic opens the trailer that locates it.
const (
	blobMagic    = "GOSTDBLB"
	trailerMagic = "GOSTDEMB"
	trailerSize  = 64
)

// Entry locates one file inside a blob: Offset is relative to the blob's
// first byte.
type Entry struct {
	Name   string
	Offset int64
	Size   int64
}

// Package describes one standard-library package of a target, as the go
// command needs it to build against without a source tree.
type Package struct {
	ImportPath string
	Name       string
	Imports    []string
	BuildID    string
	Archive    string
}

// Manifest is the standard library of one target, in dependency order.
type Manifest struct {
	Target   string
	Packages []Package
}

// Trailer is the record at the end of the file: where the blob is and what
// it hashes to.
type Trailer struct {
	Offset int64
	Size   int64
	Sum    [32]byte
}

// IsSelf reports whether name resolves inside this binary.
func IsSelf(name string) bool { return strings.HasPrefix(name, Prefix) }

// Name strips the prefix.
func Name(name string) string { return strings.TrimPrefix(name, Prefix) }

// StdArchive names the archive entry of a package for a target such as
// "cosmo_amd64".
func StdArchive(target, importPath string) string {
	return "std/" + target + "/" + importPath + ".a"
}

// ManifestEntry names the manifest entry of a target.
func ManifestEntry(target string) string { return "manifest/" + target }

// IncludeDir is the entry directory holding the assembly headers.
const IncludeDir = "include"

// blob is the parsed index of this executable's blob.
type blob struct {
	exe     string
	trailer Trailer
	entries map[string]Entry
}

var (
	once    sync.Once
	loaded  *blob
	loadErr error
)

// load parses the trailer and index of this executable once.
func load() (*blob, error) {
	once.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			loadErr = err
			return
		}
		loaded, loadErr = openBlob(exe)
	})
	return loaded, loadErr
}

// openBlob reads the trailer and index of the blob appended to the file.
func openBlob(exe string) (*blob, error) {
	file, err := os.Open(exe)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	trailer, err := ReadTrailer(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", exe, err)
	}
	entries, err := readIndex(io.NewSectionReader(file, trailer.Offset, trailer.Size))
	if err != nil {
		return nil, fmt.Errorf("%s: embedded standard library: %w", exe, err)
	}
	return &blob{exe: exe, trailer: trailer, entries: entries}, nil
}

// ErrNone reports a file that carries no embedded standard library.
var ErrNone = errors.New("no embedded standard library")

// ReadTrailer reads the trailer at the end of file.
func ReadTrailer(file *os.File) (Trailer, error) {
	info, err := file.Stat()
	if err != nil {
		return Trailer{}, err
	}
	if info.Size() < trailerSize {
		return Trailer{}, ErrNone
	}
	var raw [trailerSize]byte
	if _, err := file.ReadAt(raw[:], info.Size()-trailerSize); err != nil {
		return Trailer{}, err
	}
	if string(raw[:8]) != trailerMagic {
		return Trailer{}, ErrNone
	}
	trailer := Trailer{
		Offset: int64(binary.LittleEndian.Uint64(raw[8:16])),
		Size:   int64(binary.LittleEndian.Uint64(raw[16:24])),
	}
	copy(trailer.Sum[:], raw[24:56])
	if trailer.Offset < 0 || trailer.Size < 0 || trailer.Offset+trailer.Size > info.Size()-trailerSize {
		return Trailer{}, fmt.Errorf("embedded standard library trailer names %d bytes at %d, past the file's %d", trailer.Size, trailer.Offset, info.Size())
	}
	return trailer, nil
}

// EncodeTrailer encodes the trailer for a blob of size bytes at offset.
func EncodeTrailer(offset, size int64, sum [32]byte) []byte {
	raw := make([]byte, trailerSize)
	copy(raw, trailerMagic)
	binary.LittleEndian.PutUint64(raw[8:16], uint64(offset))
	binary.LittleEndian.PutUint64(raw[16:24], uint64(size))
	copy(raw[24:56], sum[:])
	return raw
}

// readIndex parses the index at the head of a blob.
func readIndex(blob io.ReaderAt) (map[string]Entry, error) {
	var head [16]byte
	if _, err := blob.ReadAt(head[:], 0); err != nil {
		return nil, fmt.Errorf("reading the blob header: %w", err)
	}
	if string(head[:8]) != blobMagic {
		return nil, fmt.Errorf("the blob opens with %q, not %q", head[:8], blobMagic)
	}
	size := int64(binary.LittleEndian.Uint64(head[8:16]))
	raw := make([]byte, size)
	if _, err := blob.ReadAt(raw, 16); err != nil {
		return nil, fmt.Errorf("reading the blob index: %w", err)
	}
	var list []Entry
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decoding the blob index: %w", err)
	}
	entries := make(map[string]Entry, len(list))
	for _, entry := range list {
		entries[entry.Name] = entry
	}
	return entries, nil
}

// Available reports whether this executable carries an embedded standard
// library.
func Available() bool {
	_, err := load()
	return err == nil
}

// Lookup answers the entry named by name, with or without the prefix, as
// the executable's path and the entry's absolute offset and size in it.
func Lookup(name string) (exe string, offset, size int64, err error) {
	name = Name(name)
	blob, err := load()
	if err != nil {
		return "", 0, 0, err
	}
	entry, found := blob.entries[name]
	if !found {
		return "", 0, 0, fmt.Errorf("%s: no embedded entry %q", blob.exe, name)
	}
	return blob.exe, blob.trailer.Offset + entry.Offset, entry.Size, nil
}

// Open opens the entry named by name and answers the executable, positioned
// nowhere in particular, with the entry's absolute offset and size. The
// caller closes the file.
func Open(name string) (file *os.File, offset, size int64, err error) {
	exe, offset, size, err := Lookup(name)
	if err != nil {
		return nil, 0, 0, err
	}
	file, err = os.Open(exe)
	if err != nil {
		return nil, 0, 0, err
	}
	return file, offset, size, nil
}

// ReadFile answers the whole entry named by name.
func ReadFile(name string) ([]byte, error) {
	file, offset, size, err := Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data := make([]byte, size)
	if _, err := file.ReadAt(data, offset); err != nil {
		return nil, fmt.Errorf("reading embedded %s: %w", Name(name), err)
	}
	return data, nil
}

// Entries lists every entry name with the given prefix, in index order of
// name.
func Entries(prefix string) ([]string, error) {
	blob, err := load()
	if err != nil {
		return nil, err
	}
	var names []string
	for name := range blob.entries {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sortStrings(names)
	return names, nil
}

// ReadManifest answers the manifest of a target.
func ReadManifest(target string) (*Manifest, error) {
	data, err := ReadFile(ManifestEntry(target))
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decoding the embedded manifest for %s: %w", target, err)
	}
	return &manifest, nil
}

// Verify hashes the blob and compares it with the trailer's sum.
func Verify() error {
	blob, err := load()
	if err != nil {
		return err
	}
	file, err := os.Open(blob.exe)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, blob.trailer.Offset, blob.trailer.Size)); err != nil {
		return err
	}
	if !bytes.Equal(hash.Sum(nil), blob.trailer.Sum[:]) {
		return fmt.Errorf("%s: the embedded standard library does not hash to what its trailer records", blob.exe)
	}
	return nil
}

// Writer assembles a blob: entries added in order, then the index written
// ahead of them so a reader finds it at the blob's head.
type Writer struct {
	entries []Entry
	data    bytes.Buffer
}

// Add appends one entry.
func (w *Writer) Add(name string, content []byte) {
	w.entries = append(w.entries, Entry{Name: name, Offset: int64(w.data.Len()), Size: int64(len(content))})
	w.data.Write(content)
	// Keep every entry 8-aligned so a reader may map it directly.
	for w.data.Len()%8 != 0 {
		w.data.WriteByte(0)
	}
}

// WriteTo writes the blob: magic, index length, index, then the entries.
func (w *Writer) WriteTo(out io.Writer) (int64, error) {
	index, err := json.Marshal(w.entries)
	if err != nil {
		return 0, err
	}
	for len(index)%8 != 0 {
		index = append(index, ' ')
	}
	head := int64(16 + len(index))
	for idx := range w.entries {
		w.entries[idx].Offset += head
	}
	index, err = json.Marshal(w.entries)
	if err != nil {
		return 0, err
	}
	for len(index)%8 != 0 {
		index = append(index, ' ')
	}
	var buf bytes.Buffer
	buf.WriteString(blobMagic)
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(index)))
	buf.Write(size[:])
	buf.Write(index)
	buf.Write(w.data.Bytes())
	written, err := out.Write(buf.Bytes())
	return int64(written), err
}

// sortStrings sorts in place without pulling sort into every tool that
// only reads entries.
func sortStrings(list []string) {
	for idx := 1; idx < len(list); idx++ {
		for back := idx; back > 0 && list[back] < list[back-1]; back-- {
			list[back], list[back-1] = list[back-1], list[back]
		}
	}
}
