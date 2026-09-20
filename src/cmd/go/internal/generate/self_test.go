// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package generate

import (
	"reflect"
	"testing"

	"cmd/go/internal/base"
)

// TestSelfGenerateCommand pins which directive words a go command carrying
// its own standard library runs out of its own executable.
func TestSelfGenerateCommand(t *testing.T) {
	base.SetSelf("/tmp/host", []string{"vet"})
	base.SetGoCommand([]string{"/tmp/host", "go"})

	tests := []struct {
		word string
		want []string
	}{
		{"go", []string{"/tmp/host", "go"}},
		{"vet", []string{"/tmp/host", "go", "tool", "vet"}},
		{"stringer", nil},
	}
	for _, test := range tests {
		got, err := selfGenerateCommand(test.word)
		if err != nil {
			t.Fatalf("selfGenerateCommand(%q): %v", test.word, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("selfGenerateCommand(%q) = %q, want %q", test.word, got, test.want)
		}
	}
}
