// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"encoding/base64"
	"testing"

	"cmd/go/internal/cache"
	"internal/cosmo/embedded"
)

// crypto/internal/fips140test holds test files alone, so it compiles to no
// archive and embedstd records the entry with an empty archive name. Reading
// that name asks the blob for entry "", which no blob carries.
func TestAnArchivelessStdPackageServesNothing(test *testing.T) {
	cases := []struct {
		name string
		pkg  *embedded.Package
		want bool
	}{
		{"absent from the manifest", nil, false},
		{"test files alone", &embedded.Package{ImportPath: "crypto/internal/fips140test"}, false},
		{"compiled", &embedded.Package{ImportPath: "fmt", Archive: "std/cosmo_amd64/fmt.a"}, true},
	}
	for _, each := range cases {
		if got := servesArchive(each.pkg); got != each.want {
			test.Errorf("%s: servesArchive = %v, want %v", each.name, got, each.want)
		}
	}
}

// A package the manifest never names and one it names with no archive are
// different answers. Reading both as absent sends the archive-less one down
// the tree path, which stops the go command on a binary that carries no tree.
func TestEmbeddedStdLookupPartsAbsentFromArchiveless(test *testing.T) {
	cases := []struct {
		name string
		pkg  *embedded.Package
		want embeddedStdOutcome
	}{
		{"absent from the manifest", nil, embeddedStdFromTree},
		{"test files alone", &embedded.Package{ImportPath: "crypto/internal/fips140test"}, embeddedStdNoArchive},
		{"compiled", &embedded.Package{ImportPath: "fmt", Archive: "std/cosmo_amd64/fmt.a"}, embeddedStdArchive},
	}
	for _, each := range cases {
		if got := embeddedStdLookup(each.pkg); got != each.want {
			test.Errorf("%s: embeddedStdLookup = %v, want %v", each.name, got, each.want)
		}
	}
}

// The shared cache certifies an entry by decoding the key's leading bytes and
// comparing them with the object's own build-id action. A plain content hash
// matches no action, so every embedded std archive was refused on write and
// scored corrupt on read: runtime/coverage recompiled on every build, however
// many builds had already compiled it.
func TestEmbeddedStdKeyLeadsWithTheArchivesOwnAction(test *testing.T) {
	var content [cache.HashSize]byte
	for idx := range content {
		content[idx] = byte(idx)
	}
	action := make([]byte, 15)
	for idx := range action {
		action[idx] = byte(0xA0 + idx)
	}
	encoded := base64.RawURLEncoding.EncodeToString(action)

	key := embeddedStdKey(content, encoded+"/"+encoded)
	if !bytes.Equal(key[:len(action)], action) {
		test.Errorf("embeddedStdKey = %x, want it to lead with the action %x", key, action)
	}
	if !bytes.Equal(key[len(action):], content[len(action):]) {
		test.Errorf("embeddedStdKey dropped the content tail: %x, want %x", key[len(action):], content[len(action):])
	}
}

// A build id the go command did not write leaves the key as the content hash
// rather than a truncated or garbage action, so two archives still never
// share one key.
func TestEmbeddedStdKeyKeepsContentWithoutAnAction(test *testing.T) {
	var content [cache.HashSize]byte
	content[0] = 0x11
	cases := []struct {
		name    string
		buildID string
	}{
		{"empty", ""},
		{"no slash", "abcdef"},
		{"undecodable action", "not base64!!/tail"},
		{"action as long as the key", base64.RawURLEncoding.EncodeToString(make([]byte, cache.HashSize)) + "/tail"},
	}
	for _, each := range cases {
		if got := embeddedStdKey(content, each.buildID); got != content {
			test.Errorf("%s: embeddedStdKey = %x, want the content hash %x", each.name, got, content)
		}
	}
}

// buildIDActionBytes answers the bytes cmd/go encoded, which is what a cache
// guard decodes back out of a key.
func TestBuildIDActionBytesDecodesTheLeadingField(test *testing.T) {
	want := []byte{1, 2, 3, 4, 5}
	if got := buildIDActionBytes(base64.RawURLEncoding.EncodeToString(want) + "/anything"); !bytes.Equal(got, want) {
		test.Errorf("buildIDActionBytes = %x, want %x", got, want)
	}
	if got := buildIDActionBytes("no-slash-here"); got != nil {
		test.Errorf("buildIDActionBytes without a slash = %x, want nil", got)
	}
}
