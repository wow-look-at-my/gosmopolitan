// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cmd/go/internal/load"
	"cmd/go/internal/work"
)

// TestLookupAnswer pins that a hosts or resolv.conf lookup is keyed on its
// answer. A line nobody asked about can differ from host to host and leaves
// the key alone; a change to what the lookup used moves it.
func TestLookupAnswer(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
			t.Fatal(err)
		}
		return path
	}
	base := write("base", "127.0.0.1 localhost\n10.1.0.4 runner-a.internal runner-a # this host\n")
	otherHost := write("other", "127.0.0.1\tLocalHost\n10.1.0.9 runner-b.internal runner-b\n")
	moved := write("moved", "127.0.0.2 localhost\n10.1.0.4 runner-a.internal runner-a\n")
	missing := filepath.Join(dir, "missing")
	empty := write("empty", "# no entries\n")

	answer := func(kind, file, query string) string {
		sum, err := lookupAnswer(kind, file, query, make(map[string][]rootCert))
		if err != nil {
			t.Fatalf("lookupAnswer(%s, %s, %q): %v", kind, file, query, err)
		}
		return string(sum[:])
	}
	cases := []struct {
		kind, query string
		left, right string
		same        bool
	}{
		{"hostsname", "localhost", base, otherHost, true},
		{"hostsname", "LOCALHOST", base, base, true},
		{"hostsname", "localhost", base, moved, false},
		{"hostsname", "absent.example", base, otherHost, true},
		{"hostsname", "absent.example", missing, empty, true},
		{"hostsaddr", "127.0.0.1", base, moved, false},
		{"hostsaddr", "::1", base, otherHost, true},
	}
	for _, c := range cases {
		left, right := answer(c.kind, c.left, c.query), answer(c.kind, c.right, c.query)
		if (left == right) != c.same {
			t.Errorf("%s %q in %s and %s: same answer = %v, want %v", c.kind, c.query, filepath.Base(c.left), filepath.Base(c.right), left == right, c.same)
		}
	}
	runnerA := write("resolv-a", "# stub\nnameserver 127.0.0.53\noptions edns0 trust-ad\nsearch a1.internal.cloudapp.net\n")
	runnerB := write("resolv-b", "# stub, another runner\nnameserver 127.0.0.53\noptions edns0 trust-ad\nsearch b2.internal.cloudapp.net\n")
	tcp := write("resolv-tcp", "nameserver 127.0.0.53\noptions edns0 trust-ad use-vc\nsearch a1.internal.cloudapp.net\n")
	resolvCases := []struct {
		kind, query string
		left, right string
		same        bool
	}{
		{"resolvorder", "", runnerA, runnerB, true},
		{"resolvservers", "", runnerA, runnerB, true},
		{"resolvservers", "", runnerA, tcp, false},
		{"resolvnames", "localhost.", runnerA, runnerB, true},
		{"resolvnames", "golang.org", runnerA, runnerB, false},
		{"resolvorder", "", runnerA, missing, false},
	}
	for _, c := range resolvCases {
		left, right := answer(c.kind, c.left, c.query), answer(c.kind, c.right, c.query)
		if (left == right) != c.same {
			t.Errorf("%s %q in %s and %s: same answer = %v, want %v", c.kind, c.query, filepath.Base(c.left), filepath.Base(c.right), left == right, c.same)
		}
	}

	if _, err := lookupAnswer("nosuchparser", base, "x", make(map[string][]rootCert)); err == nil {
		t.Errorf("lookupAnswer with an unknown parser succeeded")
	}
}

// TestRootLookupAnswer pins that a lookup in a system root directory is keyed
// on the roots it asked for. A certificate each host makes for itself, such as
// a snakeoil, leaves a verification that never chained to it alone.
func TestRootLookupAnswer(t *testing.T) {
	issue := func(name string) ([]byte, []byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: name},
			NotBefore:             time.Unix(0, 0),
			NotAfter:              time.Unix(1<<31, 0),
			IsCA:                  true,
			BasicConstraintsValid: true,
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert.RawSubject, der
	}
	caPEM, caSubject, caDER := issue("Shared Root CA")
	oilPEM, oilSubject, _ := issue("runner-a")
	otherOilPEM, _, _ := issue("runner-b")

	write := func(dir, name string, data []byte) {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	runnerA, runnerB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	write(runnerA, "ca.pem", caPEM)
	write(runnerA, "ssl-cert-snakeoil.pem", oilPEM)
	write(runnerB, "ca.pem", caPEM)
	write(runnerB, "ssl-cert-snakeoil.pem", otherOilPEM)

	answer := func(dir, query string) string {
		sum, err := lookupAnswer("x509dir", dir, query, make(map[string][]rootCert))
		if err != nil {
			t.Fatalf("lookupAnswer(x509dir, %s, %q): %v", dir, query, err)
		}
		return string(sum[:])
	}
	caSum := sha256.Sum224(caDER)
	cases := []struct {
		query string
		same  bool
	}{
		{"issuer " + hex.EncodeToString(caSubject), true},
		{"contains " + hex.EncodeToString(caSum[:]), true},
		{"issuer " + hex.EncodeToString(oilSubject), false},
		{"all", false},
	}
	for _, c := range cases {
		if same := answer(runnerA, c.query) == answer(runnerB, c.query); same != c.same {
			t.Errorf("x509dir %q on two runners: same answer = %v, want %v", c.query, same, c.same)
		}
	}
	if _, err := lookupAnswer("x509dir", runnerA, "subjects", make(map[string][]rootCert)); err == nil {
		t.Errorf("lookupAnswer with an unknown root query succeeded")
	}
}

// TestParseLookup pins the round trip of a lookup line, whose query may hold
// spaces and quotes.
func TestParseLookup(t *testing.T) {
	kind, file, query, err := parseLookup(`hostsname "a \"b\" c" /etc/my hosts`)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "hostsname" || file != "/etc/my hosts" || query != `a "b" c` {
		t.Errorf("parseLookup = %q, %q, %q", kind, file, query)
	}
	for _, bad := range []string{"hostsname", `hostsname "unterminated /etc/hosts`, `hostsname "q"`, `hostsname "q"/etc/hosts`} {
		if _, _, _, err := parseLookup(bad); err == nil {
			t.Errorf("parseLookup(%q) succeeded", bad)
		}
	}
}

// TestParsedFileInputs pins that a parser's own stat and open of a file stand
// in a test's inputs as its lookups. A stat or open the test made itself, of
// the same file, is still hashed whole.
func TestParsedFileInputs(t *testing.T) {
	dir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	hosts := filepath.Join(dir, "hosts")
	action := &work.Action{Package: &load.Package{PackagePublic: load.PackagePublic{Dir: dir, ImportPath: "m"}}}
	inputs := func(testlog string) string {
		_, lines, err := computeTestInputsID(action, []byte(string(testlogMagic)+testlog))
		if err != nil {
			t.Fatalf("computeTestInputsID: %v", err)
		}
		var kept []string
		for line := range strings.Lines(string(lines)) {
			if !strings.HasPrefix(line, "env ") {
				kept = append(kept, strings.TrimSuffix(line, "\n"))
			}
		}
		return strings.Join(kept, "\n")
	}

	parsedOnly := inputs("lookup hostsname \"localhost\" hosts\nparse stat hosts\nstat " + hosts + "\nparse open hosts\nopen " + hosts + "\n")
	if strings.Contains(parsedOnly, "open ") || strings.Contains(parsedOnly, "stat ") {
		t.Errorf("a parser's own stat and open were hashed:\n%s", parsedOnly)
	}
	if !strings.Contains(parsedOnly, `lookup hostsname "localhost" `+hosts+" ") {
		t.Errorf("the lookup is not among the inputs:\n%s", parsedOnly)
	}

	alsoRead := inputs("parse open hosts\nopen " + hosts + "\nopen " + hosts + "\n")
	if strings.Count(alsoRead, "open "+hosts+" ") != 1 {
		t.Errorf("the test's own open of the file is not hashed exactly once:\n%s", alsoRead)
	}

	saidAfter := inputs("open " + hosts + "\nparse open " + hosts + "\n")
	if strings.Contains(saidAfter, "open ") {
		t.Errorf("a read the parser claimed after it succeeded was hashed:\n%s", saidAfter)
	}
}
