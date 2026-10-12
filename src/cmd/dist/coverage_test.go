// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

const coverageListing = `net/http||40|12|
fmt||2|3|
errors||0|0|
vendor/golang.org/x/text/transform||0|0|transform_test.go examples_test.go
vendor/golang.org/x/text/unicode/norm||0|0|norm_test.go
vendor/golang.org/x/net/idna||0|0|
cmd/vendor/github.com/google/pprof/driver|github.com/google/pprof|0|0|driver_test.go
cmd/vendor/github.com/wow-look-at-my/go-shm|github.com/wow-look-at-my/go-shm|3|0|
cmd/vendor/golang.org/x/mod/zip|golang.org/x/mod|0|0|ignored_linux.go
`

func TestParseCoverage(t *testing.T) {
	cov, err := parseCoverage(coverageListing)
	if err != nil {
		t.Fatal(err)
	}
	if cov.total != 9 || cov.tested != 3 {
		t.Errorf("total, tested = %d, %d, want 9, 3", cov.total, cov.tested)
	}
	want := []untestedModule{
		{path: "golang.org/x/text", packages: 2, testFiles: 3},
		{path: "github.com/google/pprof", packages: 1, testFiles: 1},
	}
	if len(cov.untested) != len(want) {
		t.Fatalf("untested = %+v, want %+v", cov.untested, want)
	}
	for idx := range want {
		if cov.untested[idx] != want[idx] {
			t.Errorf("untested[%d] = %+v, want %+v", idx, cov.untested[idx], want[idx])
		}
	}
}

func TestCoverageFormat(t *testing.T) {
	cov, err := parseCoverage(coverageListing)
	if err != nil {
		t.Fatal(err)
	}
	report := cov.format()
	for _, want := range []string{
		coverageHeading,
		"**33.3% of packages have tests that run** (3 of 9 in std and cmd)",
		"3 packages (33.3%) have test files that cannot run",
		"| `golang.org/x/text` | 2 | 3 |",
		"| `github.com/google/pprof` | 1 | 1 |",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestParseCoverageRejectsShortLine(t *testing.T) {
	if _, err := parseCoverage("net/http|0|0\n"); err == nil {
		t.Error("parseCoverage accepted a line with three fields")
	}
}
