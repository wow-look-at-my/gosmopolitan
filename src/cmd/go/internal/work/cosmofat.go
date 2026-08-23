// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
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

// cosmoAPEBuild reports whether this build produces an APE this command
// assembles: a GOOS=cosmo build for an architecture an APE can carry, and
// not the sibling-architecture build, whose single-architecture output is
// an input to the assembly rather than a subject of it.
func cosmoAPEBuild() bool {
	return cfg.Goos == "cosmo" && cosmoFatArches[cfg.Goarch] != "" && os.Getenv("GOCOSMOFAT_INNER") == ""
}

// cosmoSiblingArch returns the architecture the sibling build must produce,
// or "" when this build needs only the primary one: GOCOSMOFAT=0, or a
// GOCOSMOPLATFORMS selection whose platforms all boot the same payload.
func cosmoSiblingArch() string {
	if !cosmoAPEBuild() {
		return ""
	}
	if arches := cosmoPlatformArches(); arches != nil {
		if len(arches) == 1 {
			return ""
		}
		return cosmoFatArches[cfg.Goarch]
	}
	if !cosmoFatEnv() {
		return ""
	}
	return cosmoFatArches[cfg.Goarch]
}

// cosmoFatEnabled reports whether go build should produce fat
// (amd64+arm64) APE binaries for the current configuration.
func cosmoFatEnabled() bool {
	return cosmoSiblingArch() != ""
}

// cosmoAssembleEnabled reports whether the freshly linked output goes
// through the linker's APE assembly step. A fat build must - that step is
// the merge - and a single-architecture build does whenever
// GOCOSMOPLATFORMS selected the platforms, so a slimmed binary is stripped,
// gets its sidecars, and carries a header matching the selection, exactly
// like the fat build it replaces.
func cosmoAssembleEnabled() bool {
	if !cosmoAPEBuild() {
		return false
	}
	return cosmoSiblingArch() != "" || cosmoPlatformArches() != nil
}

// CosmoFat, CosmoStrip and CosmoDebug return the effective GOCOSMOFAT,
// GOCOSMOSTRIP and GOCOSMODEBUG settings, the values `go env` reports.
// Each is what the build acts on rather than the raw environment string:
// GOCOSMOFAT reads "off" whenever the APE will carry one architecture,
// including when GOCOSMOPLATFORMS is what narrowed it to one.
func CosmoFat() string {
	set, _ := cosmoPlatformSpec()
	if cosmoFatEnv() && len(set.Arches()) > 1 {
		return "on"
	}
	return "off"
}

func CosmoStrip() string {
	if cosmoStripEnabled() {
		return "on"
	}
	return "off"
}

func CosmoDebug() string { return cosmoDebugMode() }

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

// parseCosmoDebugMode validates a GOCOSMODEBUG value and returns the
// debug mode it selects:
//
//	"full" (or unset): today's behavior - the shipped APE is stripped and
//	    the per-architecture debug sidecars are pristine copies of the
//	    linker's ELF outputs (runnable, cosmocc parity).
//	"slim": debug-only sidecars (the in-linker equivalent of
//	    objcopy --only-keep-debug) - symbol table and DWARF kept, contents
//	    of allocated sections dropped since the APE already ships them.
//	    Same sidecar names; the shipped APE is unchanged.
//	"min": slim's sidecar shape, and every GOOS=cosmo compile generates
//	    less DWARF in the first place: location lists and inline records
//	    are omitted (see cosmoDebugGcflags). Smallest sidecars; debuggers
//	    show <optimized out> for arguments/locals and no inlined-call
//	    frames. Runtime tracebacks and pprof are unaffected (pclntab).
//	"compact": slim sidecars, plus a compact debug view appended to the
//	    APE past its loadable span (never mapped at runtime), so debuggers
//	    can symbolize the assimilated binary with no sidecar present.
//
// The sidecar side of the mode only matters when the fat merge strips and
// writes sidecars at all: GOCOSMOSTRIP=0 and an explicit -s/-w in -ldflags
// both suppress sidecars entirely (see cosmoMergeArgs). min's compile-time
// trims apply to every GOOS=cosmo compile regardless, so even a
// GOCOSMOSTRIP=0 build carries the reduced DWARF in its embedded payloads.
func parseCosmoDebugMode(v string) (string, error) {
	switch v {
	case "", "full":
		return "full", nil
	case "slim", "min", "compact":
		return v, nil
	}
	return "", fmt.Errorf("invalid GOCOSMODEBUG value %q: must be full, slim, min, or compact (or unset)", v)
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

// cosmoDebugGcflags returns the extra compiler flags a GOCOSMODEBUG mode
// injects into every GOOS=cosmo compile. Only "min" injects any: it drops
// DWARF location lists (arguments/locals become <optimized out> in
// debuggers, -25% of a slim sidecar) and inline records (no inlined-call
// frames in debuggers, a further -12%). The pclntab is untouched, so
// runtime tracebacks, runtime/pprof, and the inline unwinding they do
// keep working.
func cosmoDebugGcflags(mode string) []string {
	if mode != "min" {
		return nil
	}
	return []string{"-dwarflocationlists=false", "-gendwarfinl=0"}
}

// cosmoBuildInit validates the GOCOSMO* environment (an invalid value stops
// the build early, not just at the assembly step) and applies the debug
// mode's compile-time DWARF trims by appending to forcedGcflags. Called
// from BuildInit.
//
// Forced flags precede the user's -gcflags in the compiler invocation and
// later flags win, so an explicit user -gcflags setting overrides the
// injected trims. The injected flags are part of the build-cache key
// (like all gcflags), so switching modes with different flags recompiles
// affected packages rather than reusing stale objects.
func cosmoBuildInit() {
	cosmoPlatformSpec() // reject an invalid GOCOSMOPLATFORMS on any build
	if cfg.Goos != "cosmo" || cfg.BuildToolchainName != "gc" {
		return
	}
	forcedGcflags = append(forcedGcflags, cosmoDebugGcflags(cosmoDebugMode())...)
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

// cosmoMergeArgs returns the linker arguments that assemble p's built
// target - and, when sibling is not empty, its sibling-architecture build -
// into the APE at p.Target, applying the default strip-and-sidecar behavior
// unless GOCOSMOSTRIP=0 or the user's -ldflags for p already specify -s/-w.
// GOCOSMODEBUG selects how much debug info the sidecars (and, for compact,
// the APE itself) carry; when the merge passes no strip flags at all there
// are no sidecars, so the mode has nothing to apply to and is deliberately
// not passed on. A GOCOSMOPLATFORMS selection rides along as
// -apeplatforms, where the linker turns it into the boot mechanisms the
// header carries and fails on any platform the payloads cannot serve.
func cosmoMergeArgs(p *load.Package, sibling string) []string {
	spec := p.Target
	if sibling != "" {
		spec += "," + sibling
	}
	args := []string{"-apefat", spec, "-o", p.Target}
	if set, explicit := cosmoPlatformSpec(); explicit {
		args = append(args, "-apeplatforms="+set.String())
	}
	if cosmoStripEnabled() && !ldflagsSpecifyStrip(p.Internal.Ldflags) {
		args = append(args, "-apestrip", "-apedbg")
		if mode := cosmoDebugMode(); mode != "full" {
			if mode == "min" {
				// min's extra reduction happens at compile time
				// (cosmoBuildInit); its merge-time sidecar
				// transform is exactly slim's, so the linker
				// only knows the three -apedbgmode values.
				mode = "slim"
			}
			args = append(args, "-apedbgmode="+mode)
		}
	}
	return args
}

// cosmoSibling is a sibling-architecture build running concurrently with
// the primary build.
//
// The two architectures share nothing that forces an ordering: different
// GOARCH, different build-cache keys, different output paths. The sibling
// used to run strictly after the primary finished only because fattening
// was written as a post-build step. Overlapping them reclaims each build's
// serial tail - cosmo links twice per architecture - and is worth ~23% of
// wall clock on a single main package (runtimeprobe, cold cache, 4 cores:
// 15.8s -> 12.2s, with user time unchanged, so it is pure overlap rather
// than extra work). Builds whose package graph already saturates the CPU,
// such as "go build std", gain nothing; the win is concentrated in exactly
// the single-binary builds people run interactively.
//
// The child's output is buffered rather than inherited: two concurrent
// builds writing to one terminal interleave their diagnostics into
// nonsense. It is replayed verbatim once the primary build is done.
type cosmoSibling struct {
	cmd    *exec.Cmd
	out    bytes.Buffer
	tmp    string
	childO string
	arch   string
	what   string // "fat build" or "fat install", for diagnostics
	dir    bool
	waited bool
}

// cosmoFatParallel reports whether the sibling-architecture build may run
// concurrently with the primary build. GOCOSMOFATSEQ=1 forces the old
// sequential behavior, which halves the peak memory of a fat build: the
// two link phases (each architecture links twice) would otherwise be able
// to overlap, and linking is the memory-hungry part.
func cosmoFatParallel() bool {
	switch os.Getenv("GOCOSMOFATSEQ") {
	case "1", "on":
		return false
	}
	return true
}

// setup creates the sibling's scratch directory and resolves the go
// command to re-execute. Cleanup is registered with base.AtExit because
// the primary build can now fail (and base.Fatalf exits) while the
// sibling is still running - without this the child would be orphaned
// and its scratch directory leaked.
func (s *cosmoSibling) setup() string {
	goCmd, err := os.Executable()
	if err != nil {
		base.Fatalf("go: cosmo %s: cannot find go command: %v", s.what, err)
	}
	tmp, err := os.MkdirTemp("", "gocosmofat")
	if err != nil {
		base.Fatalf("go: cosmo %s: %v", s.what, err)
	}
	s.tmp = tmp
	base.AtExit(s.cleanup)
	return goCmd
}

// launch starts cmd with its output buffered, or runs it to completion
// when parallel fattening is disabled.
func (s *cosmoSibling) launch(cmd *exec.Cmd) {
	cmd.Stdout = &s.out
	cmd.Stderr = &s.out
	s.cmd = cmd
	if !cosmoFatParallel() {
		s.finish(cmd.Run())
		return
	}
	if err := cmd.Start(); err != nil {
		s.finish(err)
	}
}

// wait blocks until the sibling build finishes.
func (s *cosmoSibling) wait() {
	if s == nil || s.waited {
		return
	}
	s.finish(s.cmd.Wait())
}

// finish replays the sibling's buffered output and reports failure. It
// runs after the primary build's own output, so the two never interleave.
func (s *cosmoSibling) finish(err error) {
	s.waited = true
	if s.out.Len() > 0 {
		os.Stderr.Write(s.out.Bytes())
		s.out.Reset()
	}
	if err != nil {
		base.Fatalf("go: cosmo %s: GOARCH=%s build failed: %v\n(set GOCOSMOFAT=0 for a single-architecture binary)", s.what, s.arch, err)
	}
}

// cleanup kills a still-running sibling and removes its scratch
// directory. Safe to call more than once.
func (s *cosmoSibling) cleanup() {
	if s.cmd != nil && !s.waited && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
		s.waited = true
	}
	if s.tmp != "" {
		os.RemoveAll(s.tmp)
		s.tmp = ""
	}
}

// cosmoFatSkipOutput reports whether -o names an existing non-regular,
// non-directory file (/dev/null and friends). Fattening re-reads the
// freshly written target and re-creates it, which only works for regular
// files, so starting a sibling build for one would be wasted work. The
// post-build filter in cosmoFatten still covers the per-target case.
func cosmoFatSkipOutput() bool {
	if cfg.BuildO == "" {
		return false
	}
	fi, err := os.Stat(cfg.BuildO)
	return err == nil && !fi.Mode().IsRegular() && !fi.IsDir()
}

// cosmoFatStart kicks off the sibling-architecture build that cosmoFatten
// will merge, and returns nil when fat builds are disabled or impossible.
// It runs the original go build command line with -o redirected to a
// temporary location and GOARCH flipped, so every other build flag and
// package argument is preserved exactly. Pass dir=true when targets are
// written to a -o directory, so the sibling build also uses one.
//
// Call this immediately before the primary build; the returned value goes
// to cosmoFatten afterwards.
func cosmoFatStart(dir bool) *cosmoSibling {
	if !cosmoFatEnabled() || cosmoFatSkipOutput() {
		return nil
	}
	cosmoDebugMode() // reject invalid GOCOSMODEBUG before the sibling build

	s := &cosmoSibling{arch: cosmoFatArches[cfg.Goarch], what: "fat build", dir: dir}
	goCmd := s.setup()

	s.childO = filepath.Join(s.tmp, "out")
	if dir {
		s.childO += string(os.PathSeparator)
		if err := os.Mkdir(filepath.Join(s.tmp, "out"), 0777); err != nil {
			base.Fatalf("go: cosmo fat build: %v", err)
		}
	}
	cmd := exec.Command(goCmd, rewriteOutputFlag(os.Args[1:], s.childO)...)
	cmd.Env = append(os.Environ(), "GOARCH="+s.arch, "GOCOSMOFAT_INNER=1")
	s.launch(cmd)
	return s
}

// cosmoFatten replaces each freshly built GOOS=cosmo executable (the Target
// of each main package in mains) with the assembled APE, merging in the
// sibling-architecture binary produced by s (when there is one) using the
// linker's -apefat mode. By default the assembly also strips each embedded
// payload to its loadable span and writes unstripped per-architecture debug
// sidecars (<target>.dbg, <target>.aarch64.elf) next to the output; see
// cosmoMergeArgs.
func cosmoFatten(s *cosmoSibling, mains []*load.Package) {
	if s == nil && !cosmoAssembleEnabled() {
		return
	}
	if s != nil {
		defer s.cleanup()
		s.wait()
	}

	// Assembly re-reads the freshly written target and re-creates it with
	// the merged APE. That only makes sense for a main package's regular
	// file: with -o /dev/null (or any other special file) the target reads
	// back empty and cannot be replaced, so leave the build's output as-is.
	regular := make([]*load.Package, 0, len(mains))
	for _, p := range mains {
		if p.Name != "main" || p.Target == "" {
			continue
		}
		if fi, err := os.Stat(p.Target); err == nil && !fi.Mode().IsRegular() {
			continue
		}
		regular = append(regular, p)
	}
	if len(regular) == 0 {
		return
	}

	link := base.Tool("link")
	for _, p := range regular {
		target := p.Target
		var sibling string
		if s != nil {
			sibling = s.childO
			if s.dir {
				sibling = filepath.Join(s.tmp, "out", filepath.Base(target))
			}
		}
		merge := exec.Command(link, cosmoMergeArgs(p, sibling)...)
		merge.Stdout = os.Stdout
		merge.Stderr = os.Stderr
		if err := merge.Run(); err != nil {
			base.Fatalf("go: cosmo build: assembling %s: %v", target, err)
		}
	}
}

// cosmoFatStartInstall kicks off the sibling-architecture install that
// cosmoFattenInstall will merge, and returns nil when fat builds are
// disabled. Call it immediately before the primary install, and pass
// hasMains=false when the command installs no main packages so no
// cross-architecture work is done for, say, "go install ./somelibrary".
//
// go install has no -o flag to redirect, and its pkg@version argument
// form cannot be rewritten into a go build, so the sibling build reruns
// the ORIGINAL install command line against a scratch GOPATH: package
// resolution stays identical while the cross-compiled result lands in
// $GOPATH/bin/$GOOS_$GOARCH/ (always a subdirectory - cosmo cannot be
// the platform the go tool itself runs on). GOMODCACHE keeps pointing
// at the real module cache so nothing is re-downloaded, and GOBIN is
// cleared because go install refuses cross-compilation with GOBIN set.
func cosmoFatStartInstall(hasMains bool) *cosmoSibling {
	if !cosmoFatEnabled() || !hasMains {
		return nil
	}
	cosmoDebugMode() // reject invalid GOCOSMODEBUG before the sibling build

	s := &cosmoSibling{arch: cosmoFatArches[cfg.Goarch], what: "fat install"}
	goCmd := s.setup()

	cmd := exec.Command(goCmd, os.Args[1:]...)
	cmd.Env = append(os.Environ(),
		"GOOS=cosmo",
		"GOARCH="+s.arch,
		"GOCOSMOFAT_INNER=1",
		"GOPATH="+s.tmp,
		"GOMODCACHE="+cfg.GOMODCACHE,
		"GOBIN=",
	)
	s.launch(cmd)
	return s
}

// cosmoFattenInstall replaces each freshly installed GOOS=cosmo
// executable (the Target of each main package in mains) with the assembled
// APE, the go-install counterpart of cosmoFatten.
func cosmoFattenInstall(s *cosmoSibling, mains []*load.Package) {
	if s == nil && !cosmoAssembleEnabled() {
		return
	}
	if s != nil {
		defer s.cleanup()
		s.wait()
	}
	if len(mains) == 0 {
		return
	}

	link := base.Tool("link")
	for _, p := range mains {
		target := p.Target
		var sibling string
		if s != nil {
			sibling = filepath.Join(s.tmp, "bin", "cosmo_"+s.arch, filepath.Base(target))
		}
		merge := exec.Command(link, cosmoMergeArgs(p, sibling)...)
		merge.Stdout = os.Stdout
		merge.Stderr = os.Stderr
		if err := merge.Run(); err != nil {
			base.Fatalf("go: cosmo install: assembling %s: %v", target, err)
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
