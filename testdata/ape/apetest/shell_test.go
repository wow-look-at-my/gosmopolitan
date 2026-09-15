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

// Every dd in the script reads bytes out of the APE. None writes any back:
// an assimilation dd names an output file and passes conv=notrunc, and the
// APE is never rewritten, in place or in a copy.
func TestShellDdOnlyReads(t *testing.T) {
	header := first8K(t)

	assert.False(t, bytes.Contains(header, []byte("conv=notrunc")),
		"conv=notrunc means a dd that writes a header over a file")
	assert.False(t, regexp.MustCompile(`dd\s+if=\S+\s+of=`).Match(header),
		"no dd in the script may name an output file")
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

	assert.Contains(t, header, `for c in "${APE_LOADER:-}" /usr/local/lib/ape/$l /usr/lib/ape/$l; do`,
		"APE_LOADER and the two system directories are searched before PATH")
	assert.Contains(t, header, `for n in $l apeld ape; do`,
		"the PATH search ends at `ape`, the cosmo loader, which boots the file too")
	assert.Contains(t, header, `exec "$c" "$o" "$@"`, "the loader is handed the APE's own path")
}

// An APE is one file. Nothing beside it is searched, because a loader
// shipped next to the binary is a second thing to carry, which is the
// property an APE exists to avoid.
func TestShellLooksForNoSidecar(t *testing.T) {
	header := string(first8K(t))

	assert.NotContains(t, header, `${o%/*}/.`, "no candidate may sit beside the binary")
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

// Registering the loader with the kernel is what lets an APE start with
// nothing writable and no loader file: F makes the kernel hold the
// interpreter by descriptor, so the path it was registered under stops
// mattering. Without F the entry names a path that a read-only host may
// not have.
func TestShellRegistersTheLoaderWithTheKernel(t *testing.T) {
	header := string(first8K(t))

	assert.Contains(t, header, `printf ":APE:M::MZqFpD=\047::$1:F"`,
		`the entry must carry F, and the magic's quote must stay octal inside DOUBLE quotes so it is not read as a boot header`)
	assert.Contains(t, header, `> /proc/sys/fs/binfmt_misc/register; } 2>/dev/null`,
		"the redirect belongs inside the group: a shell reports a redirect it cannot open on its own stderr")
	assert.Contains(t, header, `[ -e /proc/sys/fs/binfmt_misc/APE ] && return 0`,
		"an entry already registered must be left alone")
	assert.Contains(t, header, `apereg "$u"`, "the unpacked loader must be registered too")
}

// A host with nowhere to unpack and no loader must say which loader it wants
// and how to supply it. Silence there reads as a broken binary.
func TestShellNamesTheMissingLoader(t *testing.T) {
	header := string(first8K(t))

	assert.Contains(t, header, `install it on PATH, or point APE_LOADER at it`,
		"the refusal must name the fix")
	assert.Regexp(t, `no \$l on this host`, header, "the refusal must name the loader")
}

// Nothing in the script copies the program, on any host. A copy needs a
// writable filesystem, and every platform now boots through a loader that
// reads the APE where it lies.
func TestShellNeverCopiesTheProgram(t *testing.T) {
	header := string(first8K(t))

	for _, s := range []string{`cp "$o"`, `stat -L`, `cksum <"$o"`} {
		assert.NotContains(t, header, s, "%s belongs to staging a copy, which no host does", s)
	}
}
