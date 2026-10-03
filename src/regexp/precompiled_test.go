// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regexp

import (
	"reflect"
	"testing"
)

var variablePattern = []string{`a(?P<name>b+)c|d`}

// TestConstantPatternIsPrecompiled checks that the compiler built the
// Regexp of a constant pattern. Two calls share one program, which a
// run-time compile never does: it builds a new program on every call.
func TestConstantPatternIsPrecompiled(t *testing.T) {
	first := MustCompile(`a(?P<name>b+)c|d`)
	second := MustCompile(`a(?P<name>b+)c|d`)
	if first == second {
		t.Fatal("two MustCompile calls returned the same Regexp")
	}
	if first.prog != second.prog {
		t.Fatal("the constant pattern compiled at run time")
	}
	run := MustCompile(variablePattern[0])
	if run.prog == first.prog {
		t.Fatal("a variable pattern shares the precompiled program")
	}
	if !reflect.DeepEqual(*first, *run) {
		t.Fatal("the precompiled Regexp differs from the run-time one")
	}

	first.Longest()
	if second.longest {
		t.Error("Longest changed another Regexp")
	}
	first.SubexpNames()[1] = "changed"
	if second.SubexpNames()[1] != "name" {
		t.Error("SubexpNames is shared between Regexps")
	}

	posix, err := CompilePOSIX(`a|ab`)
	if err != nil || !posix.longest || posix.FindString("ab") != "ab" {
		t.Errorf("CompilePOSIX of a constant: %v, longest %v", err, posix.longest)
	}
	if _, err := Compile(`a(`); err == nil {
		t.Error("Compile of an invalid constant returned no error")
	}
}

// The benchmark patterns. A benchmark that names one of these constants
// gets the precompiled Regexp. One that reads benchPatterns compiles at run
// time.
const (
	benchLiteral = `hello`
	benchTypical = `^([a-z0-9._-]+)@([a-z0-9-]+)\.(com|org|net)$`
	benchEmail   = "^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$"
	benchURL     = `^(https?)://([a-zA-Z0-9.-]+)(?::([0-9]{1,5}))?(/[^\s?#]*)?(?:\?([^\s#]*))?(?:#(\S*))?$`
)

var benchPatterns = map[string]string{
	"literal": benchLiteral,
	"typical": benchTypical,
	"email":   benchEmail,
	"url":     benchURL,
}

var benchInputs = map[string]string{
	"literal": "say hello to the world",
	"typical": "john.doe@example.com",
	"email":   "john.doe+tag@mail.example-domain.org",
	"url":     "https://example.com:8080/path/to/page?q=1&r=2#frag",
}

var benchSink *Regexp

// precompiledBench returns the precompiled Regexp of each benchmark pattern.
// Each call builds new ones.
func precompiledBench() map[string]*Regexp {
	return map[string]*Regexp{
		"literal": MustCompile(benchLiteral),
		"typical": MustCompile(benchTypical),
		"email":   MustCompile(benchEmail),
		"url":     MustCompile(benchURL),
	}
}

// BenchmarkMustCompile compares the cost of MustCompile with a constant
// pattern, which copies a precompiled template, to a run-time compile.
func BenchmarkMustCompile(b *testing.B) {
	run := func(name string, build func() *Regexp) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchSink = build()
			}
		})
	}
	run("literal/precompiled", func() *Regexp { return MustCompile(benchLiteral) })
	run("literal/runtime", func() *Regexp { return MustCompile(benchPatterns["literal"]) })
	run("typical/precompiled", func() *Regexp { return MustCompile(benchTypical) })
	run("typical/runtime", func() *Regexp { return MustCompile(benchPatterns["typical"]) })
	run("email/precompiled", func() *Regexp { return MustCompile(benchEmail) })
	run("email/runtime", func() *Regexp { return MustCompile(benchPatterns["email"]) })
	run("url/precompiled", func() *Regexp { return MustCompile(benchURL) })
	run("url/runtime", func() *Regexp { return MustCompile(benchPatterns["url"]) })
}

// BenchmarkPrecompiledMatch checks that a precompiled Regexp matches as fast
// as a Regexp that compiled at run time.
func BenchmarkPrecompiledMatch(b *testing.B) {
	pre := precompiledBench()
	for _, name := range []string{"literal", "typical", "email", "url"} {
		input := benchInputs[name]
		for _, kind := range []string{"precompiled", "runtime"} {
			compiled := pre[name]
			if kind == "runtime" {
				compiled = MustCompile(benchPatterns[name])
			}
			if !compiled.MatchString(input) {
				b.Fatalf("%s does not match %q", name, input)
			}
			b.Run(name+"/"+kind, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					compiled.MatchString(input)
				}
			})
		}
	}
}
