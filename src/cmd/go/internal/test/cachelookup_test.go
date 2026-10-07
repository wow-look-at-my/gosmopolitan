// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		sum, err := lookupAnswer(kind, file, query)
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

	if _, err := lookupAnswer("nosuchparser", base, "x"); err == nil {
		t.Errorf("lookupAnswer with an unknown parser succeeded")
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
}
