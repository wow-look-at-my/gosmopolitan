// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-s3-server/cacheclient/cachedisk"
)

// The go command describes each action it is about to run, and the hash of
// that description is the key it stores the result under. What a key means is
// the go command's, so it is computed here rather than in the cache.

// HashSize is the number of bytes in a hash.
const HashSize = cachedisk.HashSize

// A Hash builds a cache key out of the pieces of an action's description. The
// implementation is salted SHA256, and a caller must not depend on that.
type Hash struct {
	h    hash.Hash
	name string        // for debugging
	buf  *bytes.Buffer // for verify
}

// hashSalt is a salt string added to the beginning of every hash
// created by NewHash. Using the Go version makes sure that different
// versions of the go command (or even different Git commits during
// work on the development branch) do not address the same cache
// entries, so that a bug in one version does not affect the execution
// of other versions. This salt will result in additional ActionID files
// in the cache, but not additional copies of the large output files,
// which are still addressed by unsalted SHA256.
//
// We strip any GOEXPERIMENTs the go tool was built with from this
// version string on the assumption that they shouldn't affect go tool
// execution. This allows bootstrapping to converge faster: dist builds
// go_bootstrap without any experiments, so by stripping experiments
// go_bootstrap and the final go binary will use the same salt.
var hashSalt = []byte(stripExperiment(runtime.Version()))

// stripExperiment strips any GOEXPERIMENT configuration from the Go
// version string.
func stripExperiment(version string) string {
	if i := strings.Index(version, " X:"); i >= 0 {
		return version[:i]
	}
	if i := strings.Index(version, "-X:"); i >= 0 {
		return version[:i]
	}
	return version
}

// Subkey returns an action ID corresponding to mixing a parent
// action ID with a string description of the subkey.
func Subkey(parent ActionID, desc string) ActionID {
	h := sha256.New()
	h.Write([]byte("subkey:"))
	h.Write(parent[:])
	h.Write([]byte(desc))
	var out ActionID
	h.Sum(out[:0])
	if cachedisk.HashDebug() {
		fmt.Fprintf(os.Stderr, "HASH subkey %x %q = %x\n", parent, desc, out)
	}
	if cachedisk.Verifying() {
		cachedisk.RecordHash(out, fmt.Sprintf("subkey %x %q", parent, desc))
	}
	return out
}

// NewHash returns a new Hash.
// The caller is expected to Write data to it and then call Sum.
func NewHash(name string) *Hash {
	h := &Hash{h: sha256.New(), name: name}
	if cachedisk.HashDebug() {
		fmt.Fprintf(os.Stderr, "HASH[%s]\n", h.name)
	}
	h.Write(hashSalt)
	if cachedisk.Verifying() {
		h.buf = new(bytes.Buffer)
	}
	return h
}

// Write writes data to the running hash.
func (h *Hash) Write(b []byte) (int, error) {
	if cachedisk.HashDebug() {
		fmt.Fprintf(os.Stderr, "HASH[%s]: %q\n", h.name, b)
	}
	if h.buf != nil {
		h.buf.Write(b)
	}
	return h.h.Write(b)
}

// Sum answers the hash of everything written so far.
func (h *Hash) Sum() [HashSize]byte {
	var out [HashSize]byte
	h.h.Sum(out[:0])
	if cachedisk.HashDebug() {
		fmt.Fprintf(os.Stderr, "HASH[%s]: %x\n", h.name, out)
	}
	if h.buf != nil {
		// The cache keeps this, so a mismatch it finds later can be reported
		// as what should have been there rather than as two opaque ids.
		cachedisk.RecordHash(out, h.buf.String())
	}
	return out
}

var hashFileCache struct {
	sync.Mutex
	m map[string][HashSize]byte
}

// FileHash returns the hash of the named file.
// It caches repeated lookups for a given file,
// and the cache entry for a file can be initialized
// using SetFileHash.
// The hash used by FileHash is not the same as
// the hash used by NewHash.
func FileHash(file string) ([HashSize]byte, error) {
	hashFileCache.Lock()
	out, ok := hashFileCache.m[file]
	hashFileCache.Unlock()

	if ok {
		return out, nil
	}

	h := sha256.New()
	f, err := os.Open(file)
	if err != nil {
		if cachedisk.HashDebug() {
			fmt.Fprintf(os.Stderr, "HASH %s: %v\n", file, err)
		}
		return [HashSize]byte{}, err
	}
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		if cachedisk.HashDebug() {
			fmt.Fprintf(os.Stderr, "HASH %s: %v\n", file, err)
		}
		return [HashSize]byte{}, err
	}
	h.Sum(out[:0])
	if cachedisk.HashDebug() {
		fmt.Fprintf(os.Stderr, "HASH %s: %x\n", file, out)
	}

	SetFileHash(file, out)
	return out, nil
}

// SetFileHash sets the hash returned by FileHash for file.
func SetFileHash(file string, sum [HashSize]byte) {
	hashFileCache.Lock()
	if hashFileCache.m == nil {
		hashFileCache.m = make(map[string][HashSize]byte)
	}
	hashFileCache.m[file] = sum
	hashFileCache.Unlock()
}
