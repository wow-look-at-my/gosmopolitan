// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"slices"
	"testing"
)

func TestWithoutCmdGo(t *testing.T) {
	rest, alone := withoutCmdGo([]string{"archive/tar", "cmd/go", "cmd/go/internal/work", "strings"})
	if !alone {
		t.Fatal("cmd/go was in the list, and withoutCmdGo did not see it")
	}
	want := []string{"archive/tar", "cmd/go/internal/work", "strings"}
	if !slices.Equal(rest, want) {
		t.Fatalf("rest = %q, want %q", rest, want)
	}

	rest, alone = withoutCmdGo([]string{"strings"})
	if alone {
		t.Fatal("cmd/go was not in the list, and withoutCmdGo saw it")
	}
	if !slices.Equal(rest, []string{"strings"}) {
		t.Fatalf("rest = %q, want [strings]", rest)
	}

	rest, alone = withoutCmdGo([]string{"cmd/go"})
	if !alone || len(rest) != 0 {
		t.Fatalf("cmd/go alone: rest = %q, alone = %v", rest, alone)
	}
}
