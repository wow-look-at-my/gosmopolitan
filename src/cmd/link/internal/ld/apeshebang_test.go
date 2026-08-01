// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// setAPEShebang sets -apeshebang for one test.
func setAPEShebang(t *testing.T, v bool) {
	t.Helper()
	old := *flagApeShebang
	*flagApeShebang = v
	t.Cleanup(func() { *flagApeShebang = old })
}

// apeHeaderBothWays builds one fat APE header from the same payloads with
// and without -apeshebang.
func apeHeaderBothWays(t *testing.T) (mz, shebang []byte) {
	t.Helper()
	amdElf, armElf := buildTestELFPair(t)
	build := func(sh bool) []byte {
		setAPEShebang(t, sh)
		amd, err := payloadFromELF(amdElf)
		if err != nil {
			t.Fatal(err)
		}
		arm, err := payloadFromELF(armElf)
		if err != nil {
			t.Fatal(err)
		}
		payloads := []*apePayload{amd, arm}
		layoutAPE(payloads)
		return makeAPEHeaderForPayloads(payloads)
	}
	return build(false), build(true)
}

// -apeshebang is a heading, not a second APE format: it changes the preamble
// and the one script branch that becomes untrue without an MZ (the cmd.exe
// delegation), and nothing else. Everything the shell never parses -- heredoc
// opener, e_lfanew, PE image, embedded boot ELF headers, Mach-O header, the
// loader source, the payloads -- is byte-identical, which is what lets the
// same build be re-headed the other way.
func TestAPEShebangChangesOnlyTheHeading(t *testing.T) {
	mz, shebang := apeHeaderBothWays(t)

	if len(mz) != len(shebang) {
		t.Fatalf("header sizes differ: %d vs %d", len(mz), len(shebang))
	}
	if bytes.Equal(mz[:0x2D], shebang[:0x2D]) {
		t.Fatal("-apeshebang did not change the preamble at all")
	}

	// Everything between the heredoc opener and the script -- e_lfanew and
	// the PE image header -- is untouched.
	if !bytes.Equal(mz[0x2D:apeScriptOffset], shebang[0x2D:apeScriptOffset]) {
		t.Error("the heredoc opener or PE image header changed")
	}

	// Past the preamble the first difference must be the Windows branch:
	// the dispatch, the boot-header printfs and the Mach-O dd all precede
	// it, so this pins them unchanged without enumerating them.
	i := 0x2D
	for ; i < len(mz); i++ {
		if mz[i] != shebang[i] {
			break
		}
	}
	if i == len(mz) {
		t.Fatal("the two scripts are identical; the cmd.exe delegation must not survive")
	}
	if !bytes.HasPrefix(mz[i:], []byte("# Windows shells")) {
		t.Errorf("headers first diverge at %#x, before the Windows branch: %q",
			i, mz[i:min(i+40, len(mz))])
	}
}

// The heading is a property of the header alone: the same payloads land at
// the same offsets with the same bytes either way.
func TestAPEShebangLeavesPayloadsIdentical(t *testing.T) {
	amdElf, armElf := buildTestELFPair(t)

	write := func(sh bool) []byte {
		setAPEShebang(t, sh)
		amd, err := payloadFromELF(amdElf)
		if err != nil {
			t.Fatal(err)
		}
		arm, err := payloadFromELF(armElf)
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "ape.com")
		writeAPEFile(out, []*apePayload{amd, arm})
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	mz, shebang := write(false), write(true)
	if len(mz) != len(shebang) {
		t.Fatalf("APE sizes differ: %d vs %d", len(mz), len(shebang))
	}
	if !bytes.Equal(mz[apeHeaderSize:], shebang[apeHeaderSize:]) {
		t.Error("payload region differs between headings")
	}
}

// The kernel's script loader reads a bounded first line and hands the file to
// the interpreter named there. Everything this test pins is a hard
// requirement of that path, not style.
func TestAPEShebangFirstLine(t *testing.T) {
	_, header := apeHeaderBothWays(t)

	nl := bytes.IndexByte(header, '\n')
	if nl < 0 {
		t.Fatal("no newline in header")
	}
	first := header[:nl]

	if got, want := string(header[:2]), "#!"; got != want {
		t.Errorf("header starts %q, want %q", got, want)
	}
	if got, want := string(header[:nl+1]), apeMagicShebang; got != want {
		t.Errorf("first line = %q, want %q", got, want)
	}
	// BINPRM_BUF_SIZE is 128 on Linux and the line must fit with its
	// newline; every other unix is at least as generous.
	if nl+1 > 128 {
		t.Errorf("shebang line is %d bytes, past the kernel's 128-byte buffer", nl+1)
	}
	if bytes.IndexByte(first, 0) >= 0 {
		t.Error("first line contains a NUL")
	}
	if _, err := os.Stat("/bin/sh"); err != nil && runtime.GOOS != "windows" {
		t.Errorf("interpreter /bin/sh is not present on this host: %v", err)
	}
}

// The preamble past the shebang must be a comment: the bytes are inert
// padding to the heredoc opener, and the shell would otherwise try to run
// them as a command.
func TestAPEShebangPreambleIsAComment(t *testing.T) {
	_, header := apeHeaderBothWays(t)

	for i := len(apeMagicShebang); i < 0x2D; i++ {
		if header[i] != '#' {
			t.Fatalf("preamble byte %#x = %#x, want '#'", i, header[i])
		}
	}
	if header[0x2D] != '\n' {
		t.Fatalf("byte 0x2D = %#x, want the newline that closes the comment", header[0x2D])
	}
	if !bytes.HasPrefix(header[0x2D:], []byte("\n: <<'__APE__'\n")) {
		t.Fatalf("heredoc opener missing at 0x2D: %q", header[0x2D:0x3C])
	}
	// The heredoc body holds e_lfanew's NUL bytes; nothing before it may.
	if i := bytes.IndexByte(header[:0x3C], 0); i >= 0 {
		t.Fatalf("NUL at %#x, inside the shell-parsed preamble", i)
	}
}

// A regression guard for the default: the MZ magic is what boots the same
// file natively on Windows, so nothing may flip the default heading.
func TestAPEDefaultHeadingStaysMZ(t *testing.T) {
	header, _ := apeHeaderBothWays(t)

	if got := string(header[:len(apeMagicMZ)]); got != apeMagicMZ {
		t.Errorf("default heading = %q, want %q", got, apeMagicMZ)
	}
	if header[8] != '\n' {
		t.Errorf("byte 8 = %#x, want a newline (shells binary-check the first line)", header[8])
	}
}

// The prologue is a shell script before it is anything else. A malformed
// preamble -- an unclosed quote, a comment that is not one -- would only
// surface as a runtime failure on the far side, so parse it here.
func TestAPEShebangPrologueParsesAsShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	_, header := apeHeaderBothWays(t)

	// Cut at the end of the dispatch script; past it lies binary padding
	// that no shell ever reaches (every branch execs or exits first).
	window := header[:8192]
	end := bytes.LastIndex(window, []byte("\nexit 1\n"))
	if end < 0 {
		t.Fatal("no script terminator found in the header")
	}
	script := window[:end+len("\nexit 1\n")]

	f := filepath.Join(t.TempDir(), "prologue.sh")
	if err := os.WriteFile(f, script, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", "-n", f).CombinedOutput()
	if err != nil {
		t.Fatalf("prologue is not valid shell: %v\n%s", err, out)
	}
}

// Without an MZ there is no PE image for cmd.exe to load, so the Windows
// branch must say so rather than delegating into a "not a valid Win32
// application" error from a shell the user did not know they were in.
func TestAPEShebangDoesNotDelegateToWindows(t *testing.T) {
	mz, shebang := apeHeaderBothWays(t)

	if !bytes.Contains(mz[:8192], []byte("exec cmd //c")) {
		t.Error("default header lost its cmd.exe delegation")
	}
	if bytes.Contains(shebang[:8192], []byte("exec cmd //c")) {
		t.Error("shebang header still delegates to cmd.exe, which cannot load it")
	}
	if !bytes.Contains(shebang[:8192], []byte("drops Windows support")) {
		t.Error("shebang header does not explain the Windows failure")
	}
}

// The fat merge reads thin APEs back. It recognized them by the MZ magic
// alone, so a GOCOSMOSHEBANG=1 fat build failed at the merge with a
// "not an ELF" error on its own linker's output.
func TestAPEShebangThinInputIsIngestedByTheMerge(t *testing.T) {
	setAPEShebang(t, true)

	amdElf, armElf := buildTestELFPair(t)
	dir := t.TempDir()
	thin := filepath.Join(dir, "amd64.com")
	p, err := payloadFromELF(amdElf)
	if err != nil {
		t.Fatal(err)
	}
	writeAPEFile(thin, []*apePayload{p})

	data, err := os.ReadFile(thin)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAPEHead(data) {
		t.Fatal("a shebang thin APE is not recognized as an APE")
	}
	got, err := payloadFromAPEOrELF(data)
	if err != nil {
		t.Fatalf("merge cannot ingest a shebang thin APE: %v", err)
	}
	if !bytes.Equal(got.elf, amdElf) {
		t.Error("payload recovered from a shebang thin APE differs from the input ELF")
	}
	if got.head == nil {
		t.Error("thin APE head not kept; the fat merge needs it for the PE transplant")
	}

	// And the pair still merges, which is the path a real fat build takes.
	armPath := filepath.Join(dir, "arm64.elf")
	if err := os.WriteFile(armPath, armElf, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fat.com")
	apeFatMerge(thin+","+armPath, out)
	fat, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(fat[:len(apeMagicShebang)]); got != apeMagicShebang {
		t.Errorf("merged fat APE heading = %q, want %q", got, apeMagicShebang)
	}
}
