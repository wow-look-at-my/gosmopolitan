// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"bytes"
	"strings"
	"sync"
)

// A reason long enough to clear minSerialReasonLen, so a case that is about
// one of the other rules is not also reported for its length.
const okReason = "GOMAXPROCS is process-wide and another runner would change what this counts"

func checkOne(reason ...string) []string {
	var r serialReasonRegistry
	return r.check("TestSomething", "/src/pkg/thing_test.go", 10, reason)
}

func TestSerialReasonMissing(t *T) {
	got := checkOne()
	if len(got) != 1 || !strings.Contains(got[0], "no reason given") {
		t.Fatalf("no reason: got %q, want one warning naming the omission", got)
	}
	if w := checkOne(okReason); len(w) != 0 {
		t.Errorf("a reason that breaks no rule warned: %q", w)
	}
}

func TestSerialReasonTooShort(t *T) {
	short := strings.Repeat("a", minSerialReasonLen-1)
	got := checkOne(short)
	if len(got) != 1 || !strings.Contains(got[0], "is the minimum") {
		t.Fatalf("short reason: got %q, want one length warning", got)
	}
	if w := checkOne(strings.Repeat("b", minSerialReasonLen)); len(w) != 0 {
		t.Errorf("a reason of exactly the minimum length warned: %q", w)
	}
	// The bound counts characters, not bytes, so a multi-byte reason is not
	// short just because it is dense.
	runes := strings.Repeat("\u00e9", minSerialReasonLen)
	if w := checkOne(runes); len(w) != 0 {
		t.Errorf("a %d-rune reason warned as short: %q", minSerialReasonLen, w)
	}
}

func TestSerialReasonEchoesNameOrFile(t *T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		{"the test's own name", "TestSomething holds a lock that every other caller in this package also takes", true},
		{"the file it sits in", "thing_test.go writes a package global that its neighbours read back", true},
		{"the file without its extension", "thing_test sets an option that outlives the call and nothing restores", true},
		{"case does not hide it", "testsomething leaves the seed advanced, which the next reader depends on", true},
		{"prose that names neither", okReason, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *T) {
			got := checkOne(tc.reason)
			echoed := false
			for _, w := range got {
				if strings.Contains(w, "repeats") {
					echoed = true
				}
			}
			if echoed != tc.want {
				t.Errorf("echo reported = %v, want %v; warnings %q", echoed, tc.want, got)
			}
		})
	}
}

// A subtest element too short to be a word of its own must not turn ordinary
// prose into a self-reference.
func TestSerialReasonShortNameElementIgnored(t *T) {
	var r serialReasonRegistry
	got := r.check("TestDial/tcp", "/src/net/dial_test.go", 3,
		[]string{"the resolver cache is a package global that survives between calls"})
	for _, w := range got {
		if strings.Contains(w, "repeats") {
			t.Errorf("the 3-character name element %q matched prose: %q", "tcp", w)
		}
	}
}

func TestSerialReasonSimilarity(t *T) {
	var r serialReasonRegistry
	const first = "the profile rate is a process-wide setting that another test would move under this one"

	if w := r.check("TestOne", "/src/a_test.go", 1, []string{first}); len(w) != 0 {
		t.Fatalf("the first reason warned with nothing to compare against: %q", w)
	}
	// A verbatim paste, from a different site.
	got := r.check("TestTwo", "/src/a_test.go", 2, []string{first})
	if len(got) != 1 || !strings.Contains(got[0], "the same as the one TestOne gives") {
		t.Fatalf("a verbatim paste: got %q, want one similarity warning naming TestOne", got)
	}
	// Punctuation and case are not a way to make one reason look like two.
	got = r.check("TestThree", "/src/a_test.go", 3, []string{strings.ToUpper(first) + "!!!"})
	if len(got) != 1 || !strings.Contains(got[0], "the same as the one") {
		t.Fatalf("a recased paste: got %q, want one similarity warning", got)
	}
	// A reason that says something else clears the bound.
	if w := r.check("TestFour", "/src/a_test.go", 4,
		[]string{"the working directory is per-process, so a concurrent open would resolve somewhere else"}); len(w) != 0 {
		t.Errorf("a distinct reason warned: %q", w)
	}
}

// One call site registers once, so a Serial in a loop or in a table-driven
// subtest never reports itself as its own duplicate.
func TestSerialReasonSameSiteRepeats(t *T) {
	var r serialReasonRegistry
	for i := range 3 {
		if w := r.check("TestLoop", "/src/a_test.go", 7, []string{okReason}); len(w) != 0 {
			t.Fatalf("iteration %d of one call site warned: %q", i, w)
		}
	}
}

func TestSerialReasonBreaksEveryRuleAtOnce(t *T) {
	var r serialReasonRegistry
	r.check("TestOther", "/src/a_test.go", 1, []string{okReason})
	// Short, and an echo of the test's own name.
	got := r.check("TestShort", "/src/a_test.go", 2, []string{"TestShort is slow"})
	if len(got) != 2 {
		t.Fatalf("got %d warnings, want one for the length and one for the echo: %q", len(got), got)
	}
}

func TestSerialReasonJoinsItsArguments(t *T) {
	var r serialReasonRegistry
	if w := r.check("TestJoin", "/src/a_test.go", 1,
		[]string{"the signal disposition is per-process,", "so another test would see this handler"}); len(w) != 0 {
		t.Errorf("two fragments that are long enough together warned: %q", w)
	}
}

func TestFoldSerialReason(t *T) {
	cases := []struct{ in, want string }{
		{"The Heap Goal!", "the heap goal"},
		{"  a   b  ", "a b"},
		{"a-b_c", "a b c"},
		{"", ""},
		{"...", ""},
	}
	for _, tc := range cases {
		if got := foldSerialReason(tc.in); got != tc.want {
			t.Errorf("foldSerialReason(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSimilarity(t *T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"abc", "abc", 1},
		{"", "", 1},
		{"abc", "", 0},
		{"abcd", "abce", 0.75},
		{strings.Repeat("x", 100), strings.Repeat("x", 99) + "y", 0.99},
	}
	for _, tc := range cases {
		if got := similarity(tc.a, tc.b); got != tc.want {
			t.Errorf("similarity(%.10q, %.10q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	if similarity("ab", "ba") != similarity("ba", "ab") {
		t.Error("similarity is not symmetric")
	}
}

func TestBaseName(t *T) {
	cases := []struct{ in, want string }{
		{"/src/testing/a_test.go", "a_test.go"},
		{`C:\src\testing\a_test.go`, "a_test.go"},
		{"a_test.go", "a_test.go"},
	}
	for _, tc := range cases {
		if got := baseName(tc.in); got != tc.want {
			t.Errorf("baseName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The registry is reached from every test at once, so its bookkeeping has to
// hold under the concurrency the fork's own default creates.
func TestSerialReasonRegistryConcurrent(t *T) {
	var r serialReasonRegistry
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.check("TestConcurrent", "/src/a_test.go", i, []string{okReason})
		}(i)
	}
	wg.Wait()
	if len(r.all) != 16 {
		t.Errorf("registered %d sites, want 16", len(r.all))
	}
}

// The whole path, through the exported method: a Serial call with no reason
// warns the test that made it, and the test still runs.
func TestSerialWarnsThroughT(t *T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	root := &T{
		common: common{w: &lockedWriter{mu: &mu, w: &buf}},
		tstate: newTestState(1, allMatcher()),
	}
	root.chatty = newChattyPrinter(root.w)
	ran := false
	root.Run("Sub", func(t *T) {
		t.Serial()
		ran = true
	})
	if !ran {
		t.Fatal("the subtest did not run")
	}
	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "warning: t.Serial: no reason given") {
		t.Errorf("output did not carry the warning:\n%s", out)
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(b)
}
