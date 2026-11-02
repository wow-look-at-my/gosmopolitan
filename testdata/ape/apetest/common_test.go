package apetest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

var testBinary []byte

// binPath returns the path of the APE binary under test.
func binPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("FIZZBUZZ_BIN")
	require.NotEmpty(t, path, "FIZZBUZZ_BIN environment variable must be set")
	return path
}

func loadBinary(t *testing.T) []byte {
	t.Helper()
	if testBinary != nil {
		return testBinary
	}
	var err error
	testBinary, err = os.ReadFile(binPath(t))
	require.NoError(t, err)
	return testBinary
}

const elfOffset = 0x10000

func extractELF(t *testing.T) []byte {
	t.Helper()
	bin := loadBinary(t)
	require.Greater(t, len(bin), elfOffset, "binary too small for ELF offset")
	return bin[elfOffset:]
}

func first8K(t *testing.T) []byte {
	t.Helper()
	bin := loadBinary(t)
	if len(bin) < 8192 {
		return bin
	}
	return bin[:8192]
}

// loaderRegion is one dd the bootstrap script runs to copy an embedded loader out.
type loaderRegion struct {
	block, offset, size int
	gzipped             bool
	name                string // the loader's file name, such as apeld-darwin-arm64
}

var loaderDD = regexp.MustCompile(`dd if="\$o" bs=(\d+) skip=(\d+) count=(\d+) 2>/dev/null (\| gzip -dc )?>`)

// loaderRegions reads every loader copy out of the script, with dd's block counts turned into bytes.
func loaderRegions(t *testing.T) []loaderRegion {
	t.Helper()
	var out []loaderRegion
	head := first8K(t)
	for _, at := range loaderDD.FindAllSubmatchIndex(head, -1) {
		var nums [3]int
		for idx := range nums {
			num, err := strconv.Atoi(string(head[at[2*idx+2]:at[2*idx+3]]))
			require.NoError(t, err)
			nums[idx] = num
		}
		// The section that copies a loader first sets l to the loader's name.
		name := ""
		if mark := bytes.LastIndex(head[:at[0]], []byte("l=apeld-")); mark >= 0 {
			name = string(head[mark+2 : mark+bytes.IndexByte(head[mark:], '\n')])
		}
		out = append(out, loaderRegion{
			name:    name,
			block:   nums[0],
			offset:  nums[0] * nums[1],
			size:    nums[0] * nums[2],
			gzipped: at[8] >= 0,
		})
	}
	return out
}

func le16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
func le64(b []byte) uint64 { return uint64(le32(b)) | uint64(le32(b[4:]))<<32 }

// commandForAPE builds the command that runs an APE on this host: Windows
// boots it through its PE header, and every other host goes through a shell,
// because the kernel has no handler for a file starting with MZqFpD='.
func commandForAPE(ctx context.Context, bin string, shellArgs, args []string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, bin, args...)
	}
	return exec.CommandContext(ctx, "/bin/sh", shellArgs...)
}
