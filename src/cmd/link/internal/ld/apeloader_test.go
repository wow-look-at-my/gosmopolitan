// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"compress/gzip"
	"io"
	"regexp"
	"strings"
	"testing"
)

// apeLoaderSource decompresses the loader C source the linker embeds.
func apeLoaderSource(t *testing.T) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(apeM1SourceGz))
	if err != nil {
		t.Fatalf("ape-m1.c.gz does not decompress: %v", err)
	}
	defer zr.Close()
	src, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading ape-m1.c.gz: %v", err)
	}
	return string(src)
}

// TestApeLoaderScanStartsAtTheImage pins the lower bound of the loader's
// live-memory scan to the lowest PT_LOAD. A bound of zero walks from the
// start of the address space, meets the loader's own Mach-O text, and
// refuses every ET_EXEC image.
//
// This reads the source rather than running an APE: a machine caches the
// compiled loader under the loader's version alone, so whichever APE runs
// first decides the loader every later APE reuses.
func TestApeLoaderScanStartsAtTheImage(t *testing.T) {
	src := apeLoaderSource(t)

	scan := regexp.MustCompile(`for \(a = (\w+) & -pagesz, b = \(virtmax`)
	m := scan.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("could not find the live-memory scan loop in ape-m1.c; if it moved, move this test with it")
	}
	bound := m[1]

	// The bound is a minimum, so something has to seed it: either it is
	// set from the first PT_LOAD, or it is initialised above every
	// address a segment can carry.
	seeded := regexp.MustCompile(`if \(!` + bound + ` \|\| p\[i\]\.p_vaddr < ` + bound + `\)`)
	if !seeded.MatchString(src) {
		t.Errorf("the scan runs from %s, and nothing seeds it from the first PT_LOAD.\n"+
			"A minimum initialised to zero is never lowered by an unsigned p_vaddr, so the scan\n"+
			"walks from address zero and refuses every fixed image. Seed it, or give the scan a\n"+
			"bound that is seeded.", bound)
	}

	// virtmin sizes the ET_DYN reservation (virtmax - virtmin), so the
	// scan must not borrow it: a PIE whose first PT_LOAD sits above zero
	// would then reserve less than it maps.
	if bound == "virtmin" {
		t.Error("the scan uses virtmin, which also sizes the ET_DYN reservation; give the scan its own bound")
	}

	if !strings.Contains(src, "ELF load range holds live memory") {
		t.Error("the scan's refusal message is gone; a silent scan is worse than none")
	}
}
