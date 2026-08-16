package apetest

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellMagic(t *testing.T) {
	bin := loadBinary(t)

	// MZqFpD=' = 4d 5a 71 46 70 44 3d 27
	expected := []byte("MZqFpD='")
	assert.Equal(t, expected, bin[:8], "APE must start with MZqFpD=' magic")
}

func TestShellMagicFollowedByNewline(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 9)

	// Spec: magic should be immediately followed by newline for shell binary safety
	assert.Equal(t, byte('\n'), bin[8], "magic must be followed by newline (0x0a)")
}

func TestShellFirstLineNoNUL(t *testing.T) {
	bin := loadBinary(t)

	// Find first newline
	newlinePos := bytes.IndexByte(bin, '\n')
	require.Greater(t, newlinePos, 0, "must have newline in header")

	firstLine := bin[:newlinePos]
	assert.NotContains(t, firstLine, byte(0), "first line must not contain NUL bytes")
}

func TestShellPrintfWithELFMagic(t *testing.T) {
	header := first8K(t)

	// Spec: printf statement MUST appear in first 8192 bytes
	printfPos := bytes.Index(header, []byte("printf '"))
	require.GreaterOrEqual(t, printfPos, 0, "must contain printf statement in first 8K")

	// Must contain ELF magic as octal: \177ELF
	elfMagic := []byte("\\177ELF")
	assert.True(t, bytes.Contains(header, elfMagic), "printf must contain \\177ELF magic")
}

func TestShellPrintfOctalOnly(t *testing.T) {
	header := first8K(t)

	// Find printf content
	printfStart := bytes.Index(header, []byte("printf '"))
	if printfStart < 0 {
		t.Skip("no printf found")
	}

	printfEnd := bytes.IndexByte(header[printfStart+8:], '\'')
	if printfEnd < 0 {
		t.Skip("printf not properly terminated")
	}

	printfContent := header[printfStart : printfStart+8+printfEnd+1]

	// Spec: MUST NOT use shortcuts like \n, \t, \r
	badEscapes := regexp.MustCompile(`\\[ntrbfva]`)
	assert.False(t, badEscapes.Match(printfContent), "printf must use octal escapes only, not \\n, \\t, etc.")
}

func TestShellDdStatement(t *testing.T) {
	header := first8K(t)

	// Spec: dd if="$o" of="$o" bs=... skip=... count=... conv=notrunc
	ddPattern := regexp.MustCompile(`dd\s+if=.*of=.*bs=(\d+)\s+skip=(\d+)\s+count=(\d+).*conv=notrunc`)
	match := ddPattern.FindSubmatch(header)
	require.NotNil(t, match, "must contain dd statement for Mach-O relocation")

	// bs should be 8 (standard)
	assert.Equal(t, "8", string(match[1]), "dd bs should be 8")
}

func TestShellArchDetection(t *testing.T) {
	header := first8K(t)

	assert.True(t, bytes.Contains(header, []byte("uname -m")), "must use uname -m for arch detection")
	assert.True(t, bytes.Contains(header, []byte("x86_64")), "must handle x86_64 arch")
	assert.True(t, bytes.Contains(header, []byte("amd64")), "must handle amd64 arch")
}

func TestShellMacOSDetection(t *testing.T) {
	header := first8K(t)

	assert.True(t, bytes.Contains(header, []byte("/Applications")), "must detect macOS via /Applications")
}

func TestShellExecReexecution(t *testing.T) {
	header := first8K(t)

	// Should exec itself after transformation
	execPattern := regexp.MustCompile(`exec\s+"\$`)
	assert.True(t, execPattern.Match(header), "must use exec for re-execution")
}

func TestShellVariableAssignment(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 8)

	// Bytes 6-7 should be =' (0x3D 0x27)
	assert.Equal(t, byte('='), bin[6])
	assert.Equal(t, byte('\''), bin[7])
}

// The kernel cannot exec an APE as it stands, so something must write a real
// header over its first bytes. The script writes them into a copy it stages,
// never into the file it is running: that file is often read-only, its
// checksum is what a consumer verifies, and a fat APE stops being fat the
// moment one platform's header lands on it.
func TestShellNeverWritesToItself(t *testing.T) {
	header := first8K(t)

	assert.NotContains(t, string(header), `exec 7<> "$o"`, "the ELF header must go to the staged copy, not to $o")
	assert.NotContains(t, string(header), `of="$o"`, "the Mach-O header must go to the staged copy, not to $o")
}

func TestShellStagesACopyAndExecsIt(t *testing.T) {
	header := string(first8K(t))

	assert.Contains(t, header, `cp "$o" "$p.$$"`, "must copy itself before correcting the header")
	assert.Contains(t, header, `mv -f "$p.$$" "$p"`, "must publish the copy atomically, so a concurrent first run cannot read a half-written one")
	assert.Contains(t, header, `exec "$p" "$@"`, "must exec the staged copy")
	assert.Contains(t, header, `if [ ! -x "$p" ]; then`, "must reuse an already staged copy")
}

// The copy is keyed by the identity of the file it came from, so a rebuilt
// binary never runs its predecessor's copy.
func TestShellKeysTheCopyByFileIdentity(t *testing.T) {
	header := string(first8K(t))

	assert.Contains(t, header, `stat -c %d.%i.%Y.%s "$o"`, "GNU stat: device, inode, mtime, size")
	assert.Contains(t, header, `stat -f %d.%i.%m.%z "$o"`, "BSD stat spells the same fields differently")
	assert.Contains(t, header, `cksum <"$o"`, "a host without stat falls back to the contents")
}
