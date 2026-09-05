// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"fmt"
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

// serialReason records one accepted reason and where it came from. The site
// keys the registry, so a Serial call in a loop or a table-driven subtest
// registers once and never reports itself as its own duplicate.
type serialReason struct {
	site   string // "file.go:line" of the Serial call
	test   string // the test that called Serial
	text   string // the reason as written
	folded string // text, normalized for comparison
}

var serialReasons struct {
	sync.Mutex
	byNormalized map[string]serialReason // folded text -> first site that used it
	seenSites    map[string]bool         // sites already registered
	all          []serialReason
}

// checkSerialReason validates the reason a test gave for stopping every other
// test, and reports every rule it breaks against that test. Each report is a
// warning: the test still runs alone, because refusing to serialize a test
// that asked for it would run it against the very state it is guarding.
func (t *T) checkSerialReason(reason []string, file string, line int) {
	site := fmt.Sprintf("%s:%d", baseName(file), line)

	if len(reason) == 0 {
		t.serialWarnf("no reason given. Pass one: t.Serial(%q). Serializing costs every other test in the package.",
			"why this test cannot share the process with any other")
		return
	}
	text := strings.Join(reason, " ")

	if n := len([]rune(text)); n < minSerialReasonLen {
		t.serialWarnf("reason is %d characters, and %d is the minimum: %q. Say what state is shared and who else touches it.",
			n, minSerialReasonLen, text)
	}

	// The name and the file are on the screen already, next to this warning.
	// A reason that repeats them spends its length saying nothing.
	for _, echo := range serialSelfReferences(t.Name(), file) {
		if containsFold(text, echo) {
			t.serialWarnf("reason names %q, which the reader already has from the test's own name and file: %q.", echo, text)
			break
		}
	}

	folded := foldSerialReason(text)

	serialReasons.Lock()
	defer serialReasons.Unlock()
	if serialReasons.seenSites[site] {
		return
	}
	for _, other := range serialReasons.all {
		sim := similarity(folded, other.folded)
		if sim <= maxSerialReasonSimilarity {
			continue
		}
		t.serialWarnf("reason is %.0f%% the same as the one %s gives at %s (the bound is %.0f%%): %q. Two tests that serialize for the same reason usually want t.Fork, which stops nobody.",
			sim*100, other.test, other.site, maxSerialReasonSimilarity*100, text)
		break
	}
	if serialReasons.byNormalized == nil {
		serialReasons.byNormalized = make(map[string]serialReason)
		serialReasons.seenSites = make(map[string]bool)
	}
	r := serialReason{site: site, test: t.Name(), text: text, folded: folded}
	serialReasons.seenSites[site] = true
	serialReasons.all = append(serialReasons.all, r)
	if _, ok := serialReasons.byNormalized[folded]; !ok {
		serialReasons.byNormalized[folded] = r
	}
}

// serialWarnf reports one broken rule against the calling test. It is a log
// line, not a failure: the point is to make a blanket opt-out of parallelism
// visible to whoever reads the run, not to break a suite over its prose.
func (t *T) serialWarnf(format string, args ...any) {
	t.Helper()
	t.Logf("warning: t.Serial: "+format, args...)
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
