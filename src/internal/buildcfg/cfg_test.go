// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buildcfg

import (
	"os"
	"testing"
)

func TestConfigFlags(t *testing.T) {
	os.Setenv("GOAMD64", "v1")
	if goamd64() != 1 {
		t.Errorf("Wrong parsing of GOAMD64=v1")
	}
	os.Setenv("GOAMD64", "v4")
	if goamd64() != 4 {
		t.Errorf("Wrong parsing of GOAMD64=v4")
	}
	Error = nil
	os.Setenv("GOAMD64", "1")
	if goamd64(); Error == nil {
		t.Errorf("Wrong parsing of GOAMD64=1")
	}

	os.Setenv("GORISCV64", "rva20u64")
	if goriscv64() != 20 {
		t.Errorf("Wrong parsing of RISCV64=rva20u64")
	}
	os.Setenv("GORISCV64", "rva22u64")
	if goriscv64() != 22 {
		t.Errorf("Wrong parsing of RISCV64=rva22u64")
	}
	os.Setenv("GORISCV64", "rva23u64")
	if goriscv64() != 23 {
		t.Errorf("Wrong parsing of RISCV64=rva23u64")
	}
	Error = nil
	os.Setenv("GORISCV64", "rva22")
	if _ = goriscv64(); Error == nil {
		t.Errorf("Wrong parsing of RISCV64=rva22")
	}
	Error = nil
	os.Setenv("GOARM64", "v7.0")
	if _ = goarm64(); Error == nil {
		t.Errorf("Wrong parsing of GOARM64=7.0")
	}
	Error = nil
	os.Setenv("GOARM64", "8.0")
	if _ = goarm64(); Error == nil {
		t.Errorf("Wrong parsing of GOARM64=8.0")
	}
	Error = nil
	os.Setenv("GOARM64", "v8.0,lsb")
	if _ = goarm64(); Error == nil {
		t.Errorf("Wrong parsing of GOARM64=v8.0,lsb")
	}
	os.Setenv("GOARM64", "v8.0,lse")
	if goarm64().Version != "v8.0" || goarm64().LSE != true || goarm64().Crypto != false {
		t.Errorf("Wrong parsing of GOARM64=v8.0,lse")
	}
	os.Setenv("GOARM64", "v8.0,crypto")
	if goarm64().Version != "v8.0" || goarm64().LSE != false || goarm64().Crypto != true {
		t.Errorf("Wrong parsing of GOARM64=v8.0,crypto")
	}
	os.Setenv("GOARM64", "v8.0,crypto,lse")
	if goarm64().Version != "v8.0" || goarm64().LSE != true || goarm64().Crypto != true {
		t.Errorf("Wrong parsing of GOARM64=v8.0,crypto,lse")
	}
	os.Setenv("GOARM64", "v8.0,lse,crypto")
	if goarm64().Version != "v8.0" || goarm64().LSE != true || goarm64().Crypto != true {
		t.Errorf("Wrong parsing of GOARM64=v8.0,lse,crypto")
	}
	os.Setenv("GOARM64", "v9.0")
	if goarm64().Version != "v9.0" || goarm64().LSE != true || goarm64().Crypto != false {
		t.Errorf("Wrong parsing of GOARM64=v9.0")
	}
}

func TestGowasm(t *testing.T) {
	os.Setenv("GOWASM", "")
	if f := gowasm(); f.TailCall {
		t.Errorf("Wrong parsing of GOWASM=: tailcall enabled")
	}
	os.Setenv("GOWASM", "tailcall")
	if f := gowasm(); !f.TailCall {
		t.Errorf("Wrong parsing of GOWASM=tailcall")
	} else if s := f.String(); s != "tailcall" {
		t.Errorf("gowasmFeatures.String() = %q, want %q", s, "tailcall")
	}
	os.Setenv("GOWASM", "satconv,signext,tailcall")
	if f := gowasm(); !f.TailCall {
		t.Errorf("Wrong parsing of GOWASM=satconv,signext,tailcall")
	}
	os.Setenv("GOWASM", "threads")
	if f := gowasm(); !f.Threads {
		t.Errorf("Wrong parsing of GOWASM=threads")
	} else if s := f.String(); s != "threads" {
		t.Errorf("gowasmFeatures.String() = %q, want %q", s, "threads")
	}
	os.Setenv("GOWASM", "tailcall,threads")
	if f := gowasm(); !f.TailCall || !f.Threads {
		t.Errorf("Wrong parsing of GOWASM=tailcall,threads")
	} else if s := f.String(); s != "tailcall,threads" {
		t.Errorf("gowasmFeatures.String() = %q, want %q", s, "tailcall,threads")
	}
	os.Setenv("GOWASM", "satconv")
	if f := gowasm(); f.String() != "" {
		t.Errorf("gowasmFeatures.String() for legacy features = %q, want %q", f.String(), "")
	}
	Error = nil
	os.Setenv("GOWASM", "tailcalls")
	if gowasm(); Error == nil {
		t.Errorf("Wrong parsing of GOWASM=tailcalls")
	}
	Error = nil
	os.Unsetenv("GOWASM")
}

func TestGowasi(t *testing.T) {
	os.Setenv("GOWASI", "")
	if f := gowasi(); f.WasmEdgeSock {
		t.Errorf("Wrong parsing of GOWASI=: wasmedgesock enabled")
	}
	os.Setenv("GOWASI", "wasmedgesock")
	if f := gowasi(); !f.WasmEdgeSock {
		t.Errorf("Wrong parsing of GOWASI=wasmedgesock")
	} else if s := f.String(); s != "wasmedgesock" {
		t.Errorf("gowasiFeatures.String() = %q, want %q", s, "wasmedgesock")
	}
	Error = nil
	os.Setenv("GOWASI", "wasmedgesocks")
	if gowasi(); Error == nil {
		t.Errorf("Wrong parsing of GOWASI=wasmedgesocks")
	}
	Error = nil
	os.Unsetenv("GOWASI")
}

func TestGoarm64FeaturesSupports(t *testing.T) {
	g, _ := ParseGoarm64("v9.3")

	if !g.Supports("v9.3") {
		t.Errorf("Wrong goarm64Features.Supports for v9.3, v9.3")
	}

	if g.Supports("v9.4") {
		t.Errorf("Wrong goarm64Features.Supports for v9.3, v9.4")
	}

	if !g.Supports("v8.8") {
		t.Errorf("Wrong goarm64Features.Supports for v9.3, v8.8")
	}

	if g.Supports("v8.9") {
		t.Errorf("Wrong goarm64Features.Supports for v9.3, v8.9")
	}

	if g.Supports(",lse") {
		t.Errorf("Wrong goarm64Features.Supports for v9.3, ,lse")
	}
}

func TestGogoarchTags(t *testing.T) {
	old_goarch := GOARCH
	old_goarm64 := GOARM64

	GOARCH = "arm64"

	os.Setenv("GOARM64", "v9.5")
	GOARM64 = goarm64()
	tags := gogoarchTags()
	want := []string{"arm64.v9.0", "arm64.v9.1", "arm64.v9.2", "arm64.v9.3", "arm64.v9.4", "arm64.v9.5",
		"arm64.v8.0", "arm64.v8.1", "arm64.v8.2", "arm64.v8.3", "arm64.v8.4", "arm64.v8.5", "arm64.v8.6", "arm64.v8.7", "arm64.v8.8", "arm64.v8.9"}
	if len(tags) != len(want) {
		t.Errorf("Wrong number of tags for GOARM64=v9.5")
	} else {
		for i, v := range tags {
			if v != want[i] {
				t.Error("Wrong tags for GOARM64=v9.5")
				break
			}
		}
	}

	GOARCH = old_goarch
	GOARM64 = old_goarm64
}

var goodFIPS = []string{
	"v1.0.0",
	"v1.0.1",
	"v1.2.0",
	"v1.2.3",
}

var badFIPS = []string{
	"v1.0.0-fips",
	"v1.0.0+fips",
	"1.0.0",
	"x1.0.0",
}

func TestIsFIPSVersion(t *testing.T) {
	// good
	for _, s := range goodFIPS {
		if !isFIPSVersion(s) {
			t.Errorf("isFIPSVersion(%q) = false, want true", s)
		}
	}
	// truncated
	const v = "v1.2.3"
	for i := 0; i < len(v); i++ {
		if isFIPSVersion(v[:i]) {
			t.Errorf("isFIPSVersion(%q) = true, want false", v[:i])
		}
	}
	// bad
	for _, s := range badFIPS {
		if isFIPSVersion(s) {
			t.Errorf("isFIPSVersion(%q) = true, want false", s)
		}
	}
}
