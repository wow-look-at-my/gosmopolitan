// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"

	"cmd/go/internal/cache"
	"cmd/internal/buildid"
)

// The merge that assembles an APE out of its payloads is a command outside
// the action graph, so the build cache never saw it. Its inputs are the two
// payloads, the linker, the appended blob and the merge flags, and its
// outputs are the APE and the debug sidecars the linker writes beside it.
// Keyed by the inputs, a repeated merge is a copy out of the cache.

// cosmoMergeSidecars names the sidecars a merge may write beside its output,
// by the cache subkey each is stored under.
var cosmoMergeSidecars = []string{".dbg", ".aarch64.elf"}

// cosmoMergeID computes the cache key of a merge: the linker's ID, the
// payloads' content, the blob's content and the remaining flags. A payload
// is named by its build ID, which its linker derived from its content; a
// file carrying none is hashed whole.
func cosmoMergeID(b *Builder, args []string, target, sibling string) (cache.ActionID, error) {
	h := cache.NewHash("cosmo merge")
	fmt.Fprintf(h, "cosmo merge v1\n")
	fmt.Fprintf(h, "link %s\n", b.toolID("link"))
	primary, err := cosmoPayloadID(target)
	if err != nil {
		return cache.ActionID{}, err
	}
	fmt.Fprintf(h, "primary %s\n", primary)
	if sibling != "" {
		id, err := cosmoPayloadID(sibling)
		if err != nil {
			return cache.ActionID{}, err
		}
		fmt.Fprintf(h, "sibling %s\n", id)
	}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "-apefat" || arg == "-o":
			// The payload paths, named above by content.
			i++
		case strings.HasPrefix(arg, "-apeappend="):
			sum, err := fileHash(strings.TrimPrefix(arg, "-apeappend="))
			if err != nil {
				return cache.ActionID{}, err
			}
			fmt.Fprintf(h, "append %x\n", sum)
		default:
			fmt.Fprintf(h, "arg %s\n", arg)
		}
	}
	return h.Sum(), nil
}

// cosmoPayloadID names a payload by its build ID, or by a hash of its bytes
// when it carries none.
func cosmoPayloadID(file string) (string, error) {
	if id, err := buildid.ReadFile(file); err == nil && id != "" {
		return "buildid " + id, nil
	}
	sum, err := fileHash(file)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256 %x", sum), nil
}

// fileHash returns the SHA-256 of a file's contents.
func fileHash(file string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(file)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// cosmoMergeRestore writes a cached merge's APE and sidecars to target and
// reports whether it did. Any output the cache cannot serve leaves the
// merge to run.
func cosmoMergeRestore(b *Builder, id cache.ActionID, target string) bool {
	c := cache.Default()
	ape, _, err := cache.GetFile(c, id)
	if err != nil {
		return false
	}
	manifest, _, err := cache.GetBytes(c, cache.Subkey(id, "sidecars"))
	if err != nil {
		return false
	}
	sidecars := make(map[string]string)
	for _, suffix := range strings.Fields(string(manifest)) {
		file, _, err := cache.GetFile(c, cache.Subkey(id, suffix))
		if err != nil {
			return false
		}
		sidecars[suffix] = file
	}
	sh := b.BackgroundShell()
	if err := sh.CopyFile(target, ape, 0o777, true); err != nil {
		return false
	}
	for suffix, file := range sidecars {
		if err := sh.CopyFile(target+suffix, file, 0o777, true); err != nil {
			return false
		}
	}
	return true
}

// cosmoMergeStore records a merge's APE and the sidecars it wrote, with a
// manifest naming which sidecars there are.
func cosmoMergeStore(id cache.ActionID, target string) {
	c := cache.Default()
	f, err := os.Open(target)
	if err != nil {
		return
	}
	_, _, err = c.Put(id, f)
	f.Close()
	if err != nil {
		return
	}
	var present []string
	for _, suffix := range cosmoMergeSidecars {
		s, err := os.Open(target + suffix)
		if err != nil {
			continue
		}
		_, _, err = c.Put(cache.Subkey(id, suffix), s)
		s.Close()
		if err != nil {
			return
		}
		present = append(present, suffix)
	}
	cache.PutBytes(c, cache.Subkey(id, "sidecars"), []byte(strings.Join(present, " ")))
}
