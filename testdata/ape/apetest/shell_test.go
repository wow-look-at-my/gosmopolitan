package apetest

import (
	"bytes"
	"regexp"
	"strings"
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
	// The dd statement relocates the Mach-O header, so it exists only when
	// the header does. See skipWithoutMacho.
	skipWithoutMacho(t)
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

// The kernel cannot exec an APE as it stands. A native loader reads the file
// where it lies and boots the payload from memory, so nothing has to write a
// header over the running binary: that file is often read-only, its checksum
// is what a consumer verifies, and a fat APE stops being fat the moment one
// platform's header lands on it.
func TestShellNeverWritesToItself(t *testing.T) {
	header := first8K(t)

	assert.NotContains(t, string(header), `exec 7<> "$o"`, "no boot header may be written into $o")
	assert.NotContains(t, string(header), `of="$o"`, "no Mach-O header may be written into $o")
}

// A loader the host already carries is what makes a read-only filesystem
// work: every candidate below is read, never written.
func TestShellFindsAResidentLoaderFirst(t *testing.T) {
	header := string(first8K(t))

	assert.Contains(t, header, `for c in "${APE_LOADER:-}" "${o%/*}/.$l" /usr/local/lib/ape/$l /usr/lib/ape/$l; do`,
		"APE_LOADER, a sibling of the binary, and the two system directories are searched before PATH")
	assert.Contains(t, header, `for n in $l apeld ape; do`,
		"the PATH search ends at `ape`, the cosmo loader, which boots the file too")
	assert.Contains(t, header, `exec "$c" "$o" "$@"`, "the loader is handed the APE's own path")
}

// The embedded loader is the answer for a host that carries none. It is
// unpacked once and keyed on its own content hash, so a toolchain change
// never reuses what an earlier one left behind.
func TestShellUnpacksTheEmbeddedLoader(t *testing.T) {
	header := string(first8K(t))

	unpack := regexp.MustCompile(`dd if="\$o" bs=1 skip=(\d+) count=(\d+) 2>/dev/null`)
	require.True(t, unpack.MatchString(header), "must read the embedded loader out of itself with dd")
	assert.Contains(t, header, `mv -f "$u.$$" "$u"`,
		"must publish the loader atomically, so a concurrent first run cannot exec a half-written one")
	assert.Contains(t, header, `[ -s "$u.$$" ]`, "an empty unpack must not be published as a loader")
	assert.Contains(t, header, `exec "$u" "$o" "$@"`, "must exec the unpacked loader against the APE in place")

	tag := regexp.MustCompile(`u=\$\{APE_LOADERDIR:-/tmp/\.ape-ld-1-\$\(id -u [^)]*\)[^}]*\}/\$l-[0-9a-f]{8}`)
	assert.True(t, tag.MatchString(header), "the unpack path must be per-user and carry the loader's content tag")
}

// A host with nowhere to unpack and no loader must say which loader it wants
// and how to supply it. Silence there reads as a broken binary.
func TestShellNamesTheMissingLoader(t *testing.T) {
	header := string(first8K(t))

	assert.Contains(t, header, `install it on PATH, or point APE_LOADER at it`,
		"the refusal must name the fix")
	assert.Regexp(t, `no \$l on this host`, header, "the refusal must name the loader")
}

// darwin/amd64 has no embedded loader, so it stages a copy. A host that
// carries an apeld-darwin-amd64 of its own must still be used first: that
// is the only way an APE claiming this platform starts on a read-only
// filesystem.
func TestShellSearchesBeforeItStages(t *testing.T) {
	skipWithoutMacho(t)
	header := string(first8K(t))

	search := strings.Index(header, "l=apeld-darwin-amd64")
	require.GreaterOrEqual(t, search, 0, "the staging branch must search for a darwin/amd64 loader")
	stage := strings.Index(header, `cp "$o" "$p.$$"`)
	require.GreaterOrEqual(t, stage, 0, "darwin/amd64 must still stage a copy when no loader answers")
	assert.Less(t, search, stage, "the search must come before the copy")
}

// The copy darwin/amd64 stages is keyed by the identity of the file it came
// from, so a rebuilt binary never runs what an earlier build left staged.
func TestShellKeysTheCopyByFileIdentity(t *testing.T) {
	skipWithoutMacho(t)
	header := string(first8K(t))

	assert.Contains(t, header, `stat -L -f %d.%i.%Fm.%z "$o"`, "BSD stat, -L so a symlink keys on its target: device, inode, mtime, size")
	assert.Contains(t, header, `stat -L -c %d.%i.%.9Y.%s "$o"`, "GNU stat spells the same fields differently")
	assert.Contains(t, header, `cksum <"$o"`, "a host without stat falls back to the contents")
}
