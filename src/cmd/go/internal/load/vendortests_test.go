// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package load

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsVendoredThirdParty(t *testing.T) {
	cases := map[string]bool{
		"vendor/golang.org/x/text/transform":                            true,
		"cmd/vendor/golang.org/x/tools/go/cfg":                          true,
		"cmd/vendor/github.com/google/pprof/driver":                     true,
		"cmd/vendor/github.com/wow-look-at-my/go-s3-server/cacheclient": false,
		"vendor/github.com/wow-look-at-my/example":                      false,
		"net/http":                        false,
		"cmd/go":                          false,
		"example.com/vendor/golang.org/x": false,
	}
	for path, want := range cases {
		assert.Equal(t, want, isVendoredThirdParty(path), "isVendoredThirdParty(%q)", path)
	}
}
