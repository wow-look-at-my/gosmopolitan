// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package gocmd

import "testing"

// go test and go vet parse -n after main warms the cache, so main reads it off
// the arguments. A -n meant for the test binary does not count.
func TestPrintsOnly(t *testing.T) {
	for _, row := range []struct {
		args []string
		want bool
	}{
		{[]string{"-n", "std"}, true},
		{[]string{"-c", "-o", "/dev/null", "-n", "std", "cmd"}, true},
		{[]string{"--n=true", "fmt"}, true},
		{[]string{"-v", "fmt"}, false},
		{[]string{"fmt", "-args", "-n"}, false},
		{nil, false},
	} {
		if got := printsOnly(row.args); got != row.want {
			t.Errorf("printsOnly(%q) = %v, want %v", row.args, got, row.want)
		}
	}
}
