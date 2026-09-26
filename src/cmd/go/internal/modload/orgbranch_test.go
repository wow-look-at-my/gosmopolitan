// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modload

import (
	"reflect"
	"strings"
	"testing"

	"golang.org/x/mod/module"
)

const (
	alphaPath = "github.com/wow-look-at-my/alpha"
	alphaHead = "v0.0.0-20260102030405-0f69f837cebe"
	alphaOld  = "v0.0.0-20250101000000-aaaaaaaaaaaa"
	gammaPath = "github.com/wow-look-at-my/gamma/v2"
)

func TestPinOrgRequires(t *testing.T) {
	reqs := []module.Version{
		{Path: alphaPath, Version: "v0.0.0"},
		{Path: "rsc.io/quote", Version: "v1.5.2"},
		{Path: "github.com/wow-look-at-my/beta", Version: alphaOld},
		{Path: gammaPath, Version: "v2.0.0"},
		{Path: "github.com/wow-look-at-my/delta", Version: "v0.0.0"},
	}
	recorded := map[string]string{
		alphaPath: alphaHead,
		gammaPath: "v2.0.0-20260102030405-0f69f837cebe",
	}
	want := []module.Version{
		{Path: alphaPath, Version: alphaHead},
		{Path: "rsc.io/quote", Version: "v1.5.2"},
		{Path: "github.com/wow-look-at-my/beta", Version: alphaOld},
		{Path: gammaPath, Version: "v2.0.0-20260102030405-0f69f837cebe"},
	}
	got := pinOrgRequires(reqs, recorded)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pinOrgRequires:\n got %v\nwant %v", got, want)
	}
	if reqs[0].Version != "v0.0.0" {
		t.Errorf("pinOrgRequires changed its input: %v", reqs[0])
	}
}

func TestOrgCheckRecorded(t *testing.T) {
	for _, mod := range []module.Version{
		{Path: alphaPath, Version: alphaHead},
		{Path: "rsc.io/quote", Version: "v0.0.0"},
		{Path: gammaPath, Version: "v2.0.0-20260102030405-0f69f837cebe"},
	} {
		if err := orgCheckRecorded(mod); err != nil {
			t.Errorf("orgCheckRecorded(%v) = %v, want nil", mod, err)
		}
	}
	for _, mod := range []module.Version{
		{Path: alphaPath, Version: "v0.0.0"},
		{Path: gammaPath, Version: "v2.0.0"},
		{Path: gammaPath, Version: "v0.0.0"},
	} {
		err := orgCheckRecorded(mod)
		if err == nil {
			t.Errorf("orgCheckRecorded(%v) = nil, want an error", mod)
			continue
		}
		for _, part := range []string{mod.Path, mod.Version, "run any go command outside CI"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("orgCheckRecorded(%v) = %q, want it to name %q", mod, err, part)
			}
		}
	}
}
