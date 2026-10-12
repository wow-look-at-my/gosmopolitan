package apetest

import (
	"encoding/binary"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// apeBootSpan derives the page-aligned file span the memfd loader has to
// stage for this host's payload: every PT_LOAD file range plus the
// program header table, rounded out to pages. It mirrors the loader's
// own walk: the payload is a complete ELF starting on a 64K boundary,
// and its program headers hold absolute file offsets.
func apeBootSpan(t *testing.T, ape []byte, machine uint16) (lo, hi uint64) {
	t.Helper()
	for off := uint64(0x10000); off+64 <= uint64(len(ape)); off += 0x10000 {
		p := ape[off:]
		if binary.LittleEndian.Uint32(p) != 0x464c457f || binary.LittleEndian.Uint16(p[18:]) != machine {
			continue
		}
		phoff := binary.LittleEndian.Uint64(p[32:])
		require.Equal(t, uint16(56), binary.LittleEndian.Uint16(p[54:]),
			"payload phentsize at %#x: the loader requires 64-bit program headers", off)
		phnum := uint64(binary.LittleEndian.Uint16(p[56:]))
		require.NotZero(t, phnum, "payload phnum at %#x", off)
		phabs := off + phoff
		phsz := phnum * 56
		require.LessOrEqual(t, phabs+phsz, uint64(len(ape)), "program headers at %#x run past the file", phabs)
		lo, hi = phabs, phabs+phsz
		for i := uint64(0); i < phnum; i++ {
			e := ape[phabs+i*56:]
			po := binary.LittleEndian.Uint64(e[8:])
			pf := binary.LittleEndian.Uint64(e[32:])
			if binary.LittleEndian.Uint32(e) == 1 { // PT_LOAD
				if po < lo {
					lo = po
				}
				if po+pf > hi {
					hi = po + pf
				}
			}
		}
		return lo &^ 0xfff, (hi + 0xfff) &^ 0xfff
	}
	t.Fatalf("no ELF payload for machine %d on a 64K boundary", machine)
	return 0, 0
}

// parseBootImage reads the probe's "ok bootimage staged=N file=M" line.
func parseBootImage(t *testing.T, out string) (staged, file int64) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(line, "ok bootimage staged=")
		if !ok {
			continue
		}
		stagedStr, fileStr, ok := strings.Cut(rest, " file=")
		require.True(t, ok, "malformed bootimage line %q", line)
		staged, err := strconv.ParseInt(stagedStr, 10, 64)
		require.NoError(t, err, "bootimage staged %q", stagedStr)
		file, err = strconv.ParseInt(fileStr, 10, 64)
		require.NoError(t, err, "bootimage file %q", fileStr)
		return staged, file
	}
	t.Fatal("no ok bootimage line in the probe output")
	return 0, 0
}
