// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cosmoape describes the host platforms a GOOS=cosmo APE can boot
// on and the payload architecture each one needs.
//
// cmd/go reads GOCOSMOPLATFORMS through it to decide which architectures to
// build; cmd/link reads -apeplatforms through it to decide which boot
// headers to emit. Both sides must agree on the token spelling, so the
// table lives here rather than in either command.
package cosmoape

import (
	"fmt"
	"strings"
)

// Platform is a host OS/architecture an APE can boot on. Arch names the
// PAYLOAD it boots, which is not always its own architecture: windows/amd64
// boots the cosmo amd64 image through the APE's PE header, and there is no
// separate windows payload.
type Platform struct {
	OS   string
	Arch string
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// all lists every platform this toolchain can emit boot support for, in
// canonical order. Set is a bitmask over these indices, so the order is
// also the order platforms are reported in.
var all = [...]Platform{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
}

// Named platforms, for callers that test membership of one.
var (
	LinuxAMD64   = all[0]
	LinuxARM64   = all[1]
	DarwinAMD64  = all[2]
	DarwinARM64  = all[3]
	WindowsAMD64 = all[4]
)

// Set is a set of platforms.
type Set uint

// Default is the set a build covers when GOCOSMOPLATFORMS is unset. It is
// deliberately NOT every platform in all: it is the three this fork stands
// behind, and the other two stay selectable rather than promised.
//
// linux/arm64 and darwin/amd64 are omitted because a default build should
// not claim a host nothing verifies. darwin/amd64 is the sharper case: its
// syscall surface is complete, but signal delivery is still a stub
// (rt_sigaction returns success without installing a handler) and there is
// no Intel-mac runner, so nothing there has ever been executed. An APE that
// advertised it would announce a platform on which it has never run.
//
// Naming a platform in GOCOSMOPLATFORMS still selects it. This changes what
// silence means, not what is reachable.
func Default() Set {
	return 1<<indexOf(LinuxAMD64) | 1<<indexOf(DarwinARM64) | 1<<indexOf(WindowsAMD64)
}

// indexOf returns p's bit position, panicking on a platform not in all so a
// typo in Default cannot silently produce an empty set.
func indexOf(p Platform) uint {
	for i, q := range all {
		if q == p {
			return uint(i)
		}
	}
	panic("cosmoape: platform not in table: " + p.String())
}

// Names returns the accepted platform tokens, for diagnostics.
func Names() string {
	names := make([]string, len(all))
	for i, p := range all {
		names[i] = p.String()
	}
	return strings.Join(names, ", ")
}

// Parse turns a comma-separated os/arch list into a Set. An unknown token
// or an empty entry is an error: a build that silently dropped a requested
// platform would produce a binary that dies on a user's machine with no
// symptom to search for. Callers treat an unset variable as Default rather
// than passing "" here, which Parse rejects.
func Parse(spec string) (Set, error) {
	var s Set
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			return 0, fmt.Errorf("empty platform in %q (want a comma-separated list of %s)", spec, Names())
		}
		i := -1
		for j, p := range all {
			if p.String() == tok {
				i = j
				break
			}
		}
		if i < 0 {
			return 0, fmt.Errorf("unknown platform %q (valid: %s)", tok, Names())
		}
		s |= 1 << i
	}
	return s, nil
}

// Has reports whether the set contains p.
func (s Set) Has(p Platform) bool {
	for i, q := range all {
		if q == p {
			return s&(1<<i) != 0
		}
	}
	return false
}

// Platforms returns the set's members in canonical order.
func (s Set) Platforms() []Platform {
	var ps []Platform
	for i, p := range all {
		if s&(1<<i) != 0 {
			ps = append(ps, p)
		}
	}
	return ps
}

// Arches returns the payload architectures the set needs, amd64 first.
func (s Set) Arches() []string {
	var arches []string
	for _, want := range [...]string{"amd64", "arm64"} {
		for _, p := range s.Platforms() {
			if p.Arch == want {
				arches = append(arches, want)
				break
			}
		}
	}
	return arches
}

// NeedsArch reports whether any platform in the set boots the arch payload.
func (s Set) NeedsArch(arch string) bool {
	for _, p := range s.Platforms() {
		if p.Arch == arch {
			return true
		}
	}
	return false
}

// RestrictToArches returns the subset whose platforms boot one of the given
// architectures. It is how a build with no explicit selection reports what
// it actually supports: the platforms the payloads on hand can serve.
func (s Set) RestrictToArches(arches []string) Set {
	var out Set
	for i, p := range all {
		if s&(1<<i) == 0 {
			continue
		}
		for _, a := range arches {
			if p.Arch == a {
				out |= 1 << i
				break
			}
		}
	}
	return out
}

// String returns the canonical comma-separated spelling, which Parse
// accepts back.
func (s Set) String() string {
	ps := s.Platforms()
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.String()
	}
	return strings.Join(names, ",")
}
