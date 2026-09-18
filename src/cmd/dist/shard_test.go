// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"testing"
)

func shardTester() *tester {
	tst := &tester{}
	noop := func(*distTest) error { return nil }
	for _, pkg := range []string{"archive/tar", "fmt", "os", "cmd/go"} {
		tst.addTest(pkg, stdTestHeading, noop)
	}
	tst.addTest("os/user:osusergo", "os/user with tag osusergo", noop)
	tst.addTest("net/http:nethttpomithttp2", "net/http with tag nethttpomithttp2", noop)
	tst.addTest("runtime:cpu1", "GOMAXPROCS=2 runtime -cpu=1 -quick", noop)
	tst.addTest("runtime:cpu2", "GOMAXPROCS=2 runtime -cpu=2 -quick", noop)
	tst.addTest("cmd/internal/testdir:0_2", "../test", noop)
	tst.addTest("cmd/internal/testdir:1_2", "../test", noop)
	return tst
}

func TestShardPartitionIsComplete(t *testing.T) {
	tst := shardTester()
	for count := 1; count <= 5; count++ {
		seen := make(map[string]int)
		for idx := 0; idx < count; idx++ {
			for name := range tst.shardTests(idx, count) {
				seen[name]++
			}
		}
		for _, each := range tst.tests {
			if seen[each.name] != 1 {
				t.Errorf("%d parts: %s is in %d parts, want exactly 1", count, each.name, seen[each.name])
			}
		}
		if len(seen) != len(tst.tests) {
			t.Errorf("%d parts: the parts name %d tests, want the %d registered", count, len(seen), len(tst.tests))
		}
	}
}

func TestShardPackageTestsStayTogether(t *testing.T) {
	tst := shardTester()
	for count := 2; count <= 5; count++ {
		first := tst.shardTests(0, count)
		for _, each := range tst.tests {
			isPackage := each.heading == stdTestHeading
			if first[each.name] != isPackage {
				t.Errorf("%d parts: %s in part 0 is %v, want %v", count, each.name, first[each.name], isPackage)
			}
		}
	}
}

func TestShardSpreadsTheRest(t *testing.T) {
	tst := shardTester()
	const count = 3
	sizes := make([]int, count)
	for idx := range sizes {
		sizes[idx] = len(tst.shardTests(idx, count))
	}
	if fmt.Sprint(sizes) != "[4 3 3]" {
		t.Errorf("part sizes = %v, want [4 3 3]: the 4 package tests, then the 6 others in turn", sizes)
	}
}

func TestParseShard(t *testing.T) {
	for _, spec := range []struct {
		text       string
		idx, count int
	}{
		{"0/1", 0, 1},
		{"0/2", 0, 2},
		{"1/2", 1, 2},
		{"4/7", 4, 7},
	} {
		idx, count := parseShard(spec.text)
		if idx != spec.idx || count != spec.count {
			t.Errorf("parseShard(%q) = %d, %d; want %d, %d", spec.text, idx, count, spec.idx, spec.count)
		}
	}
}
