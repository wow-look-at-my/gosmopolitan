// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"slices"
	"testing"
)

func TestSegprotRequests(test *testing.T) {
	cases := []struct {
		flags []string
		want  []segprotRequest
	}{
		{nil, nil},
		{[]string{"-static", "-Wl,-rpath,/x"}, nil},
		{[]string{"-Wl,-segprot,__TEXT,rwx,rx"}, []segprotRequest{{"__TEXT", 7, 5}}},
		{[]string{"-segprot", "__DATA", "rw-", "r"}, []segprotRequest{{"__DATA", 3, 1}}},
		{[]string{"-Wl,-x,-segprot,__TEXT,0x7,0x5,-y"}, []segprotRequest{{"__TEXT", 7, 5}}},
	}
	for _, tcase := range cases {
		got, err := segprotRequests(tcase.flags)
		if err != nil {
			test.Errorf("segprotRequests(%q): %v", tcase.flags, err)
			continue
		}
		if !slices.Equal(got, tcase.want) {
			test.Errorf("segprotRequests(%q) = %v, want %v", tcase.flags, got, tcase.want)
		}
	}
	for _, bad := range [][]string{{"-Wl,-segprot,__TEXT,rwx"}, {"-segprot", "__TEXT", "rwq", "rx"}} {
		if _, err := segprotRequests(bad); err == nil {
			test.Errorf("segprotRequests(%q) succeeded", bad)
		}
	}
}
