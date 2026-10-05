// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package orgmod

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
)

// HeadsEnv carries the heads a go command resolved to every process it
// starts. A go command that a go generate directive or a go run program
// starts takes them, so one invocation builds one set of heads and asks for
// each of them once.
const HeadsEnv = "GOSMOPOLITAN_ORG_HEADS"

// A Heads value maps module@branch to the version this process builds.
type Heads map[string]string

// ParseHeads reads the module@branch=version entries of a HeadsEnv value. The
// entries are separated by commas.
func ParseHeads(value string) (Heads, error) {
	heads := Heads{}
	for entry := range strings.SplitSeq(value, ",") {
		if entry == "" {
			continue
		}
		name, version, ok := strings.Cut(entry, "=")
		module, branch, hasBranch := strings.Cut(name, "@")
		if !ok || !hasBranch || module == "" || branch == "" || version == "" {
			return nil, fmt.Errorf("%s: entry %q: want module@branch=version", HeadsEnv, entry)
		}
		heads[name] = version
	}
	return heads, nil
}

// String formats heads as a HeadsEnv value, in a fixed order.
func (heads Heads) String() string {
	var out strings.Builder
	for _, name := range slices.Sorted(maps.Keys(heads)) {
		if out.Len() > 0 {
			out.WriteByte(',')
		}
		out.WriteString(name + "=" + heads[name])
	}
	return out.String()
}

// Inherited returns the heads the go command that started this process
// resolved. It reads the environment once.
var Inherited = sync.OnceValues(func() (Heads, error) {
	return ParseHeads(os.Getenv(HeadsEnv))
})
