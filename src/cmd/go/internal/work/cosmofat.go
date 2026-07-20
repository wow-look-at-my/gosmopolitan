// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/load"
)

// cosmoFatArches maps each fat-APE architecture to its sibling.
var cosmoFatArches = map[string]string{
	"amd64": "arm64",
	"arm64": "amd64",
}

// cosmoFatEnabled reports whether go build should produce fat
// (amd64+arm64) APE binaries for the current configuration.
func cosmoFatEnabled() bool {
	if cfg.Goos != "cosmo" {
		return false
	}
	if cosmoFatArches[cfg.Goarch] == "" {
		return false
	}
	switch os.Getenv("GOCOSMOFAT") {
	case "0", "off":
		return false
	}
	// Guard against the sibling-architecture build recursing.
	return os.Getenv("GOCOSMOFAT_INNER") == ""
}

// cosmoStripEnabled reports whether fat APE merges should strip debug info
// (DWARF, symbol table, section headers) from the shipped binary and write
// per-architecture debug sidecars next to it. On by default; GOCOSMOSTRIP=0
// opts out, mirroring GOCOSMOFAT.
func cosmoStripEnabled() bool {
	switch os.Getenv("GOCOSMOSTRIP") {
	case "0", "off":
		return false
	}
	return true
}

// parseCosmoDebugMode validates a GOCOSMODEBUG value and returns the fat
// APE debug mode it selects:
//
//	"full" (or unset): today's behavior - the shipped APE is stripped and
//	    the per-architecture debug sidecars are pristine copies of the
//	    linker's ELF outputs (runnable, cosmocc parity).
//	"slim": debug-only sidecars (the in-linker equivalent of
//	    objcopy --only-keep-debug) - symbol table and DWARF kept, contents
//	    of allocated sections dropped since the APE already ships them.
//	    Same sidecar names; the shipped APE is unchanged.
//	"compact": slim sidecars, plus a compact debug view appended to the
//	    APE past its loadable span (never mapped at runtime), so debuggers
//	    can symbolize the assimilated binary with no sidecar present.
//
// The mode only matters when the fat merge strips and writes sidecars at
// all: GOCOSMOSTRIP=0 and an explicit -s/-w in -ldflags both suppress
// sidecars entirely, making GOCOSMODEBUG a no-op (see cosmoMergeArgs).
func parseCosmoDebugMode(v string) (string, error) {
	switch v {
	case "", "full":
		return "full", nil
	case "slim", "compact":
		return v, nil
	}
	return "", fmt.Errorf("invalid GOCOSMODEBUG value %q: must be full, slim, or compact (or unset)", v)
}

// cosmoDebugMode returns the GOCOSMODEBUG mode for fat APE merges,
// stopping the build with an error for invalid values (unlike GOCOSMOFAT
// and GOCOSMOSTRIP, whose values are binary, a typo here would silently
// select a wrong debug-info shape).
func cosmoDebugMode() string {
	mode, err := parseCosmoDebugMode(os.Getenv("GOCOSMODEBUG"))
	if err != nil {
		base.Fatalf("go: %v", err)
	}
	return mode
}

// ldflagsSpecifyStrip reports whether the user's -ldflags for a package
// contain an explicit -s or -w (in any spelling the linker accepts: -s,
// --s, -s=..., and likewise for -w). When the user has taken a position on
// stripping, the fat merge passes no flags of its own: the payloads are
// embedded exactly as the user's link produced them and no sidecars are
// written.
//
// Known heuristic false positive: the separate value of a value-taking
// flag can itself begin with '-' (e.g. -ldflags="-extldflags -s") and is
// misread as the linker's -s/-w. The effect is the conservative one of
// deferring to the user: no default strip, no sidecars.
func ldflagsSpecifyStrip(ldflags []string) bool {
	for _, f := range ldflags {
		if !strings.HasPrefix(f, "-") {
			continue // a flag's value, not a flag
		}
		f = strings.TrimLeft(f, "-")
		if f == "s" || f == "w" || strings.HasPrefix(f, "s=") || strings.HasPrefix(f, "w=") {
			return true
		}
	}
	return false
}

// cosmoMergeArgs returns the linker arguments that merge p's built target
// and its sibling-architecture build into a fat APE at p.Target, applying
// the default strip-and-sidecar behavior unless GOCOSMOSTRIP=0 or the
// user's -ldflags for p already specify -s/-w. GOCOSMODEBUG selects how
// much debug info the sidecars (and, for compact, the APE itself) carry;
// when the merge passes no strip flags at all there are no sidecars, so
// the mode has nothing to apply to and is deliberately not passed on.
func cosmoMergeArgs(p *load.Package, sibling string) []string {
	args := []string{"-apefat", p.Target + "," + sibling, "-o", p.Target}
	if cosmoStripEnabled() && !ldflagsSpecifyStrip(p.Internal.Ldflags) {
		args = append(args, "-apestrip", "-apedbg")
		if mode := cosmoDebugMode(); mode != "full" {
			args = append(args, "-apedbgmode="+mode)
		}
	}
	return args
}

// cosmoFatten replaces each freshly built GOOS=cosmo executable (the Target
// of each main package in mains) with a fat (amd64+arm64) APE. It reruns
// the original go build command for the sibling cosmo architecture into a
// temporary location, then merges each pair of binaries with the linker's
// -apefat mode. By default the merge also strips each embedded payload to
// its loadable span and writes unstripped per-architecture debug sidecars
// (<target>.dbg, <target>.aarch64.elf) next to the output; see
// cosmoMergeArgs. Pass dir=true when targets were written to a -o
// directory, so the sibling build also uses one.
func cosmoFatten(mains []*load.Package, dir bool) {
	if !cosmoFatEnabled() || len(mains) == 0 {
		return
	}
	// Fattening re-reads the freshly written target and re-creates it with
	// the merged APE. That only makes sense for regular files: with
	// -o /dev/null (or any other special file) the target reads back empty
	// and cannot be replaced, so leave the primary build's output as-is.
	regular := make([]*load.Package, 0, len(mains))
	for _, p := range mains {
		if fi, err := os.Stat(p.Target); err == nil && !fi.Mode().IsRegular() {
			continue
		}
		regular = append(regular, p)
	}
	mains = regular
	if len(mains) == 0 {
		return
	}
	cosmoDebugMode() // reject invalid GOCOSMODEBUG before the sibling build
	otherArch := cosmoFatArches[cfg.Goarch]

	goCmd, err := os.Executable()
	if err != nil {
		base.Fatalf("go: cosmo fat build: cannot find go command: %v", err)
	}

	tmp, err := os.MkdirTemp("", "gocosmofat")
	if err != nil {
		base.Fatalf("go: cosmo fat build: %v", err)
	}
	defer os.RemoveAll(tmp)

	// Rerun the original command line with -o redirected to the
	// temporary location and GOARCH flipped, so every other build flag
	// and package argument is preserved exactly.
	childO := filepath.Join(tmp, "out")
	if dir {
		childO += string(os.PathSeparator)
		if err := os.Mkdir(filepath.Join(tmp, "out"), 0777); err != nil {
			base.Fatalf("go: cosmo fat build: %v", err)
		}
	}
	args := rewriteOutputFlag(os.Args[1:], childO)
	cmd := exec.Command(goCmd, args...)
	cmd.Env = append(os.Environ(), "GOARCH="+otherArch, "GOCOSMOFAT_INNER=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		base.Fatalf("go: cosmo fat build: GOARCH=%s build failed: %v\n(set GOCOSMOFAT=0 for a single-architecture binary)", otherArch, err)
	}

	link := base.Tool("link")
	for _, p := range mains {
		target := p.Target
		sibling := childO
		if dir {
			sibling = filepath.Join(tmp, "out", filepath.Base(target))
		}
		merge := exec.Command(link, cosmoMergeArgs(p, sibling)...)
		merge.Stdout = os.Stdout
		merge.Stderr = os.Stderr
		if err := merge.Run(); err != nil {
			base.Fatalf("go: cosmo fat build: merging %s: %v", target, err)
		}
	}
}

// cosmoFattenInstall replaces each freshly installed GOOS=cosmo
// executable (the Target of each main package in mains) with a fat
// (amd64+arm64) APE, the go-install counterpart of cosmoFatten.
//
// go install has no -o flag to redirect, and its pkg@version argument
// form cannot be rewritten into a go build, so the sibling build reruns
// the ORIGINAL install command line against a scratch GOPATH: package
// resolution stays identical while the cross-compiled result lands in
// $GOPATH/bin/$GOOS_$GOARCH/ (always a subdirectory - cosmo cannot be
// the platform the go tool itself runs on). GOMODCACHE keeps pointing
// at the real module cache so nothing is re-downloaded, and GOBIN is
// cleared because go install refuses cross-compilation with GOBIN set.
func cosmoFattenInstall(mains []*load.Package) {
	if !cosmoFatEnabled() || len(mains) == 0 {
		return
	}
	cosmoDebugMode() // reject invalid GOCOSMODEBUG before the sibling build
	otherArch := cosmoFatArches[cfg.Goarch]

	goCmd, err := os.Executable()
	if err != nil {
		base.Fatalf("go: cosmo fat install: cannot find go command: %v", err)
	}
	tmp, err := os.MkdirTemp("", "gocosmofat")
	if err != nil {
		base.Fatalf("go: cosmo fat install: %v", err)
	}
	defer os.RemoveAll(tmp)

	rerun := func(goos, goarch string) {
		cmd := exec.Command(goCmd, os.Args[1:]...)
		cmd.Env = append(os.Environ(),
			"GOOS="+goos,
			"GOARCH="+goarch,
			"GOCOSMOFAT_INNER=1",
			"GOPATH="+tmp,
			"GOMODCACHE="+cfg.GOMODCACHE,
			"GOBIN=",
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			base.Fatalf("go: cosmo fat install: GOOS=%s GOARCH=%s install failed: %v\n(set GOCOSMOFAT=0 for a single-architecture binary)", goos, goarch, err)
		}
	}
	rerun("cosmo", otherArch)

	link := base.Tool("link")
	for _, p := range mains {
		target := p.Target
		name := filepath.Base(target)
		sibling := filepath.Join(tmp, "bin", "cosmo_"+otherArch, name)
		merge := exec.Command(link, cosmoMergeArgs(p, sibling)...)
		merge.Stdout = os.Stdout
		merge.Stderr = os.Stderr
		if err := merge.Run(); err != nil {
			base.Fatalf("go: cosmo fat install: merging %s: %v", target, err)
		}
	}
}

// rewriteOutputFlag returns args with the value of the -o flag replaced by
// out, or with "-o out" prepended if no -o flag is present. The first
// argument is expected to be the "build" subcommand.
func rewriteOutputFlag(args []string, out string) []string {
	res := make([]string, 0, len(args)+2)
	replaced := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "--o":
			res = append(res, "-o", out)
			i++ // skip original value
			replaced = true
			continue
		case strings.HasPrefix(a, "-o=") || strings.HasPrefix(a, "--o="):
			res = append(res, "-o="+out)
			replaced = true
			continue
		}
		res = append(res, a)
	}
	if !replaced {
		// Insert after the subcommand name.
		if len(res) > 0 {
			res = append(res[:1], append([]string{"-o", out}, res[1:]...)...)
		} else {
			res = append(res, "-o", out)
		}
	}
	return res
}
