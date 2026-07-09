package apetest

import (
	"bytes"
	"compress/gzip"
	"debug/elf"
	"io"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeBootHeaders decodes every printf '...' statement in the first 8192
// bytes the same way the APE loader (ape-m1.c) does: literal bytes, with
// backslash followed by up to three octal digits as an escape, terminated by
// the first raw single quote.
func decodeBootHeaders(t *testing.T) [][]byte {
	t.Helper()
	head := first8K(t)
	var blobs [][]byte
	for i := 0; ; {
		j := bytes.Index(head[i:], []byte("printf '"))
		if j < 0 {
			break
		}
		p := i + j + 8
		var out []byte
		for p < len(head) {
			c := head[p]
			p++
			if c == '\'' {
				break
			}
			if c == '\\' {
				v := byte(0)
				for n := 0; n < 3 && p < len(head) && head[p] >= '0' && head[p] <= '7'; n++ {
					v = v*8 + head[p] - '0'
					p++
				}
				c = v
			}
			out = append(out, c)
		}
		blobs = append(blobs, out)
		i = p
	}
	return blobs
}

// bootHeaderByMachine returns the decoded boot ELF header for the given
// machine type, or nil.
func bootHeaderByMachine(t *testing.T, machine elf.Machine) []byte {
	t.Helper()
	for _, b := range decodeBootHeaders(t) {
		if len(b) >= 64 && le16(b[18:]) == uint16(machine) {
			return b
		}
	}
	return nil
}

// TestFatBootHeaders verifies that the fat APE embeds exactly one decodable
// boot ELF header per architecture in the loader's 8192-byte scan window.
func TestFatBootHeaders(t *testing.T) {
	blobs := decodeBootHeaders(t)
	require.Len(t, blobs, 2, "fat APE must embed exactly two printf boot headers")

	var machines []elf.Machine
	for _, b := range blobs {
		require.GreaterOrEqual(t, len(b), 64, "boot header must decode to at least an ELF header")
		require.Equal(t, []byte{0x7f, 'E', 'L', 'F'}, b[:4], "decoded blob must be an ELF header")
		machines = append(machines, elf.Machine(le16(b[18:])))
	}
	assert.Contains(t, machines, elf.EM_X86_64, "must embed an x86-64 boot header")
	assert.Contains(t, machines, elf.EM_AARCH64, "must embed an aarch64 boot header")
}

// TestFatPayloads verifies, for each architecture's boot header, that the
// program headers it points at are inside the file and satisfy the APE
// loader's congruence requirement (p_vaddr == p_offset mod 16384).
func TestFatPayloads(t *testing.T) {
	bin := loadBinary(t)
	for _, machine := range []elf.Machine{elf.EM_X86_64, elf.EM_AARCH64} {
		hdr := bootHeaderByMachine(t, machine)
		require.NotNil(t, hdr, "missing boot header for %v", machine)

		entry := le64(hdr[24:])
		phoff := le64(hdr[32:])
		phentsize := uint64(le16(hdr[54:]))
		phnum := uint64(le16(hdr[56:]))
		require.LessOrEqual(t, phoff+phnum*phentsize, uint64(len(bin)), "%v: program headers must be inside the file", machine)

		entryMapped := false
		for i := uint64(0); i < phnum; i++ {
			ph := bin[phoff+i*phentsize:]
			ptype := le32(ph[0:])
			pflags := le32(ph[4:])
			poff := le64(ph[8:])
			pvaddr := le64(ph[16:])
			pfilesz := le64(ph[32:])
			pmemsz := le64(ph[40:])
			if ptype != uint32(elf.PT_LOAD) {
				continue
			}
			assert.LessOrEqual(t, poff+pfilesz, uint64(len(bin)), "%v: PT_LOAD must be inside the file", machine)
			assert.Equal(t, poff&16383, pvaddr&16383, "%v: p_vaddr must be congruent to p_offset mod 16384", machine)
			if pflags&uint32(elf.PF_X) != 0 && entry >= pvaddr && entry < pvaddr+pmemsz {
				entryMapped = true
			}
		}
		assert.True(t, entryMapped, "%v: entry point must be inside an executable PT_LOAD", machine)
	}
}

// TestFatApeLoaderEmbedded verifies the gzipped APE loader source for macOS
// ARM64 is embedded at the offset the bootstrap script extracts it from.
func TestFatApeLoaderEmbedded(t *testing.T) {
	head := first8K(t)
	re := regexp.MustCompile(`dd if="\$o" bs=1 skip=(\d+) count=(\d+)`)
	m := re.FindSubmatch(head)
	require.NotNil(t, m, "bootstrap script must extract the APE loader with real offsets")

	skip, err := strconv.Atoi(string(m[1]))
	require.NoError(t, err)
	count, err := strconv.Atoi(string(m[2]))
	require.NoError(t, err)

	bin := loadBinary(t)
	require.LessOrEqual(t, skip+count, len(bin), "loader region must be inside the file")

	gz, err := gzip.NewReader(bytes.NewReader(bin[skip : skip+count]))
	require.NoError(t, err, "loader region must be valid gzip")
	src, err := io.ReadAll(gz)
	require.NoError(t, err, "loader source must decompress")
	assert.Contains(t, string(src), "ApeLoader", "decompressed source must be the APE loader")
}
