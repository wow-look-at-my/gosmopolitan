// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// printfBlobTestInput returns a blob exercising every byte value plus
// adjacency cases where an octal escape is immediately followed by an
// octal digit, which must not be absorbed into the escape.
func printfBlobTestInput() []byte {
	blob := make([]byte, 0, 256+16)
	for i := 0; i < 256; i++ {
		blob = append(blob, byte(i))
	}
	// Escaped byte followed by octal digits: '%' -> \045, then literal "7".
	blob = append(blob, '%', '7', '\'', '0', '\\', '1', 0x00, '2', 0xff, '3')
	return blob
}

// decodePrintfBlob decodes the body of a printf '...' format string the way
// both POSIX printf and the APE loader's header scanner do: a backslash
// introduces an octal escape of one to three digits, and every other byte is
// taken literally.
func decodePrintfBlob(t *testing.T, s string) []byte {
	t.Helper()
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		if i+1 >= len(s) || s[i+1] < '0' || s[i+1] > '7' {
			t.Fatalf("backslash at offset %d is not followed by an octal digit", i)
		}
		v := 0
		n := 0
		for n < 3 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' {
			v = v*8 + int(s[i+1]-'0')
			i++
			n++
		}
		out = append(out, byte(v))
	}
	return out
}

func TestWritePrintfBlobEscaping(t *testing.T) {
	blob := printfBlobTestInput()

	var script bytes.Buffer
	writePrintfBlob(&script, blob)
	enc := script.String()

	for i := 0; i < len(enc); i++ {
		c := enc[i]
		if c < 0x20 || c >= 0x7f {
			t.Errorf("offset %d: encoded byte %#02x is not printable ASCII", i, c)
		}
		switch c {
		case '%':
			// printf would interpret a bare % as a conversion directive.
			t.Errorf("offset %d: bare %% in encoded blob", i)
		case '\'':
			// A raw quote terminates both the shell string and the APE
			// loader's scan of the printf statement.
			t.Errorf("offset %d: bare single quote in encoded blob", i)
		case '\\':
			// Backslashes may appear only as octal escape lead-ins.
			if i+1 >= len(enc) || enc[i+1] < '0' || enc[i+1] > '7' {
				t.Errorf("offset %d: backslash not followed by octal digit", i)
			}
		}
	}

	got := decodePrintfBlob(t, enc)
	if !bytes.Equal(got, blob) {
		t.Errorf("decoded blob does not round-trip:\ngot  %x\nwant %x", got, blob)
	}
}

// TestWritePrintfBlobShellRoundTrip runs the encoded blob through a real
// POSIX shell's printf, the way APE self-assimilation does, and checks that
// the bytes written match the input exactly.
func TestWritePrintfBlobShellRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX sh on windows")
	}
	testenv.MustHaveExecPath(t, "sh")

	blob := printfBlobTestInput()

	var script bytes.Buffer
	writePrintfBlob(&script, blob)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.bin")
	shPath := filepath.Join(dir, "blob.sh")
	// Mirror the APE bootstrap: open an fd on the target and printf into it.
	shellScript := "exec 7<> \"$1\" || exit 121\nprintf '" + script.String() + "' >&7\n"
	if err := os.WriteFile(shPath, []byte(shellScript), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", shPath, outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh printf failed: %v\n%s", err, out)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("shell printf output does not round-trip:\ngot  %x\nwant %x", got, blob)
	}
}
