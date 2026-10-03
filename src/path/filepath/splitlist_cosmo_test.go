// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package filepath_test

import (
	"path/filepath"
	"reflect"
	"testing"
)

// The NT branch of splitList only runs on a Windows host, and one APE is
// tested wherever it was built. Passing the separator in is what lets a linux
// host exercise it: the cases are path_test.go's winsplitlisttests.
func TestSplitListQuoted(t *testing.T) {
	for _, tt := range []struct {
		list string
		want []string
	}{
		{`"a"`, []string{`a`}},
		{`a;b`, []string{`a`, `b`}},
		{`"a;b"`, []string{`a;b`}},
		{`";"`, []string{`;`}},
		{`"a;b";c`, []string{`a;b`, `c`}},
		{`c:\";a";c:\b`, []string{`c:\;a`, `c:\b`}},
	} {
		if got := filepath.SplitListQuoted(tt.list, ';'); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitListQuoted(%#q) = %#q, want %#q", tt.list, got, tt.want)
		}
	}
}
