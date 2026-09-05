// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// A serial test stops the whole suite, so the reason it gives has to say
// something a reader cannot already see. These constants are what "something"
// means.
const (
	// minSerialReasonLen is the shortest reason that is accepted without a
	// warning. It is long enough that "process-wide" alone does not reach it.
	minSerialReasonLen = 48

	// maxSerialReasonSimilarity is how alike two reasons may be. A reason
	// pasted from another test lands at 1; an edited copy of one lands just
	// under it, which is the case this bound is here to catch.
	maxSerialReasonSimilarity = 0.98
)

// serialReason records one accepted reason and the call it came from.
type serialReason struct {
	site   string // "file.go:line" of the Serial call
	test   string // the test that called Serial
	folded string // the reason, normalized for comparison
}

// serialReasonRegistry holds every reason a test binary has given so far. It
// is what makes the similarity rule mean anything: a rule that judged one
// reason in isolation would pass a file whose every test pastes the same
// sentence.
type serialReasonRegistry struct {
	mu       sync.Mutex
	sites    map[string]bool // call sites already registered
	all      []serialReason
	reported []string // warnings, for the summary at the end of the run
}

// serialReasons is the registry the running test binary shares.
var serialReasons serialReasonRegistry

// check validates one Serial call and returns the warnings it earns, in the
// order the rules are stated. It returns nothing for a reason that breaks no
// rule.
//
// A call site registers once. A Serial inside a loop or a table-driven subtest
// therefore never reports itself as its own duplicate, and the similarity rule
// stays a statement about distinct calls in the source.
func (r *serialReasonRegistry) check(testName, file string, line int, reason []string) []string {
	if len(reason) == 0 {
		return []string{fmt.Sprintf("no reason given. Pass one: t.Serial(%q). Every other test in this package stops for the duration.",
			"why this test cannot share the process")}
	}
	text := strings.Join(reason, " ")

	var warnings []string
	warnf := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}

	if n := len([]rune(text)); n < minSerialReasonLen {
		warnf("reason is %d characters and %d is the minimum: %q. Name the shared state and who else touches it.",
			n, minSerialReasonLen, text)
	}

	// The test's name and file sit next to this warning on the screen. A
	// reason that repeats either one spends its length saying nothing.
	for _, echo := range serialSelfReferences(testName, file) {
		if containsFold(text, echo) {
			warnf("reason repeats %q, which the reader already has from the test's own name and file: %q.", echo, text)
			break
		}
	}

	site := fmt.Sprintf("%s:%d", baseName(file), line)
	folded := foldSerialReason(text)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sites[site] {
		return warnings
	}
	for _, other := range r.all {
		sim := similarity(folded, other.folded)
		if sim <= maxSerialReasonSimilarity {
			continue
		}
		warnf("reason is %.1f%% the same as the one %s gives at %s, and the bound is %.0f%%: %q. Two tests serializing for one reason usually want t.Fork, which stops nobody.",
			sim*100, other.test, other.site, maxSerialReasonSimilarity*100, text)
		break
	}
	if r.sites == nil {
		r.sites = make(map[string]bool)
	}
	r.sites[site] = true
	r.all = append(r.all, serialReason{site: site, test: testName, folded: folded})
	return warnings
}

// report writes the run's collected warnings and forgets them. It is called
// once, after the tests, because a warning logged against a passing test is
// invisible without -v, and this rule is about a cost the whole package pays.
func (r *serialReasonRegistry) report(w io.Writer) {
	r.mu.Lock()
	reported := r.reported
	r.reported = nil
	r.mu.Unlock()

	if len(reported) == 0 {
		return
	}
	fmt.Fprintf(w, "testing: %d t.Serial call(s) did not justify stopping the package:\n", len(reported))
	for _, line := range reported {
		fmt.Fprintf(w, "\t%s\n", line)
	}
}

// takeReported drops the warnings collected so far and returns them. A test
// that produced a warning on purpose calls it, so the deliberate warning does
// not reach the summary at the end of the run.
func (r *serialReasonRegistry) takeReported() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	reported := r.reported
	r.reported = nil
	return reported
}

// checkSerialReason reports every rule this test's reason breaks. Each report
// is a warning and the test still runs alone: refusing to serialize a test
// that asked for it would run that test against the state it is guarding, and
// a suite must not fail over its own prose.
func (t *T) checkSerialReason(reason []string, file string, line int) {
	t.Helper()
	site := fmt.Sprintf("%s:%d", baseName(file), line)
	for _, w := range serialReasons.check(t.Name(), file, line, reason) {
		t.Logf("warning: t.Serial: %s", w)
		serialReasons.mu.Lock()
		serialReasons.reported = append(serialReasons.reported, site+": "+w)
		serialReasons.mu.Unlock()
	}
}

// serialSelfReferences returns the strings a reason must not contain: the
// test's own name, its name elements, and the file it sits in. Short elements
// are left out - a subtest called "1" or "tcp" would match ordinary prose.
func serialSelfReferences(name, file string) []string {
	const minEcho = 4 // shorter than this matches real words by accident

	refs := []string{}
	add := func(s string) {
		if len(s) >= minEcho {
			refs = append(refs, s)
		}
	}
	add(name)
	for _, elem := range strings.Split(name, "/") {
		add(elem)
	}
	base := baseName(file)
	add(base)
	add(strings.TrimSuffix(base, ".go"))
	return refs
}

// baseName returns the last element of a source path, under either separator:
// the compiler records slashes, but a path that reached here from elsewhere
// may not have them.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// foldSerialReason normalizes a reason for comparison: lowercase, with every
// run of non-alphanumeric bytes collapsed to one space. Punctuation and case
// are then not a way to make two identical reasons look different.
func foldSerialReason(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true // leading spaces are dropped
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 0x7f:
			b.WriteRune(r)
			space = false
		case !space:
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// similarity scores two normalized reasons from 0 to 1, as the share of the
// longer one that edit distance leaves untouched.
func similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	ar, br := []rune(a), []rune(b)
	longest := max(len(ar), len(br))
	// An edit distance is at least the difference in length, so a pair this
	// far apart cannot clear the bound and needs no distance computed.
	if diff := abs(len(ar) - len(br)); float64(longest-diff)/float64(longest) <= maxSerialReasonSimilarity {
		return float64(longest-diff) / float64(longest)
	}
	return float64(longest-editDistance(ar, br)) / float64(longest)
}

// editDistance is Levenshtein distance over two rows.
func editDistance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
