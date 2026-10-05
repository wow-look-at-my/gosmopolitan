// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/load"
	"cmd/go/internal/trace"
)

// cosmoFatArches maps each fat-APE architecture to its sibling.
var cosmoFatArches = map[string]string{
	"amd64": "arm64",
	"arm64": "amd64",
}

// cosmoAPEBuild reports whether this build produces an APE this command
// assembles.
func cosmoAPEBuild() bool {
	return cfg.Goos == "cosmo" && cosmoFatArches[cfg.Goarch] != "" && os.Getenv("GOCOSMOFAT_INNER") == ""
}

// cosmoSiblingArch returns the architecture the sibling build must produce,
// or "" when this build needs only the primary one. GOCOSMOFAT=0, or a
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

// cosmoAssembleEnabled reports whether the freshly linked output goes through
// the linker's APE assembly step.
func cosmoAssembleEnabled() bool {
	if !cosmoAPEBuild() {
		return false
	}
	return cosmoSiblingArch() != "" || cosmoPlatformArches() != nil
}

// CosmoFat, CosmoStrip and CosmoDebug return the effective GOCOSMOFAT,
// GOCOSMOSTRIP and GOCOSMODEBUG settings.
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

// cosmoStripEnabled reports whether fat APE merges should strip debug info (DWARF, symbol table, section headers) from the shipped binary.
func cosmoStripEnabled() bool {
	switch os.Getenv("GOCOSMOSTRIP") {
	case "0", "off":
		return false
	}
	return true
}

// parseCosmoDebugMode validates a GOCOSMODEBUG value and returns the debug
// mode it selects: "full" (or unset).
func parseCosmoDebugMode(v string) (string, error) {
	switch v {
	case "", "full":
		return "full", nil
	case "slim", "min", "compact":
		return v, nil
	}
	return "", fmt.Errorf("invalid GOCOSMODEBUG value %q: must be full, slim, min, or compact (or unset)", v)
}

// cosmoDebugMode returns the GOCOSMODEBUG mode for fat APE merges.
func cosmoDebugMode() string {
	mode, err := parseCosmoDebugMode(os.Getenv("GOCOSMODEBUG"))
	if err != nil {
		base.Fatalf("go: %v", err)
	}
	return mode
}

// cosmoDebugGcflags returns the extra compiler flags a GOCOSMODEBUG mode
// injects into every GOOS=cosmo compile.
func cosmoDebugGcflags(mode string) []string {
	if mode != "min" {
		return nil
	}
	return []string{"-dwarflocationlists=false", "-gendwarfinl=0"}
}

// cosmoBuildInit validates the GOCOSMO* environment (an invalid value stops
// the build early, not at the assembly step) and applies the debug mode's
// compile-time DWARF trims by appending.
func cosmoBuildInit() {
	cosmoPlatformSpec() // reject an invalid GOCOSMOPLATFORMS on any build
	if cfg.Goos != "cosmo" || cfg.BuildToolchainName != "gc" {
		return
	}
	forcedGcflags = append(forcedGcflags, cosmoDebugGcflags(cosmoDebugMode())...)
}

// ldflagsSpecifyStrip reports whether the user's -ldflags for a package
// contain an explicit -s or -w (in any spelling the linker accepts: -s, --s,
// -s=..., and likewise for -w).
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

// cosmoMergeArgs returns the linker arguments that assemble p's built target
// - and, when sibling is not empty, its sibling-architecture build - into the
// APE at p.Target, applying the default strip-and-sidecar behavior unless
// GOCOSMOSTRIP=0 or the user's -ldflags for p already specify -s/-w.
// GOCOSMODEBUG selects how much debug info the sidecars (and, for compact,
// the APE itself) carry. When the merge passes no strip flags at all there
// are no sidecars, so the mode has nothing to apply to. The mode is
// deliberately not passed on.
func cosmoMergeArgs(p *load.Package, sibling string) []string {
	spec := p.Target
	if sibling != "" {
		spec += "," + sibling
	}
	args := []string{"-apefat", spec, "-o", p.Target}
	if set, explicit := cosmoPlatformSpec(); explicit {
		args = append(args, "-apeplatforms="+set.String())
	}
	// GOCOSMOAPPEND names a standard library blob, written by go tool
	// embedstd, that the merged APE carries past its load span.
	if blob := os.Getenv("GOCOSMOAPPEND"); blob != "" {
		args = append(args, "-apeappend="+blob)
	}
	if cosmoStripEnabled() && !ldflagsSpecifyStrip(p.Internal.Ldflags) {
		args = append(args, "-apestrip", "-apedbg")
		if mode := cosmoDebugMode(); mode != "full" {
			if mode == "min" {
				// min's extra reduction happens at compile time (cosmoBuildInit).
				mode = "slim"
			}
			args = append(args, "-apedbgmode="+mode)
		}
	}
	return args
}

// cosmoSibling is a sibling-architecture build running concurrently with
// the primary build.
type cosmoSibling struct {
	cmd    *exec.Cmd
	out    bytes.Buffer
	tmp    string
	childO string
	arch   string
	what   string // "fat build" or "fat install", for diagnostics
	dir    bool
	waited bool

	// The sibling is a whole second go command, so in a trace it is its own row.
	ctx     context.Context
	lane    trace.Lane
	started time.Time
	trace   string
}

// cosmoFatParallel reports whether the sibling-architecture build may run
// concurrently with the primary build.
func cosmoFatParallel() bool {
	switch os.Getenv("GOCOSMOFATSEQ") {
	case "1", "on":
		return false
	}
	return true
}

// setup creates the sibling's scratch directory and resolves the go command
// to re-execute. Cleanup is registered with base.AtExit. This is because the
// primary build can now fail (and base.Fatalf exits).
func (s *cosmoSibling) setup() []string {
	goCmd, err := base.GoCommand()
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
// runs after the primary build's own output, so both never interleave.
func (s *cosmoSibling) finish(err error) {
	s.waited = true
	if s.lane.Enabled() {
		args := map[string]any{
			"arch":     s.arch,
			"what":     s.what,
			"parallel": cosmoFatParallel(),
			"output":   s.childO,
		}
		if err != nil {
			args["error"] = err.Error()
		}
		s.lane.Since("cosmo sibling build", "cosmo", s.started, args)
	}
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
// non-directory file (/dev/null and friends).
func cosmoFatSkipOutput() bool {
	if cfg.BuildO == "" {
		return false
	}
	fi, err := os.Stat(cfg.BuildO)
	return err == nil && !fi.Mode().IsRegular() && !fi.IsDir()
}

// cosmoFatStart kicks off the sibling-architecture build that cosmoFatten
// will merge, and returns nil when fat builds are disabled or impossible. It
// runs the go build command line with -o redirected to a temporary location.
// GOARCH flipped, so every other build flag and package argument is
// preserved exactly. Pass dir=true when targets are written to a -o
// directory, so the sibling build also uses one.
//
// Call this immediately before the primary build; the returned value goes to
// cosmoFatten afterwards.
func cosmoFatStart(ctx context.Context, dir bool) *cosmoSibling {
	if !cosmoFatEnabled() || cosmoFatSkipOutput() {
		return nil
	}
	cosmoDebugMode() // reject invalid GOCOSMODEBUG before the sibling build

	s := &cosmoSibling{arch: cosmoFatArches[cfg.Goarch], what: "fat build", dir: dir}
	goCmd := s.setup()
	s.traceOn(ctx) // after setup: the sibling's trace lives in its scratch dir

	s.childO = filepath.Join(s.tmp, "out")
	if dir {
		s.childO += string(os.PathSeparator)
		if err := os.Mkdir(filepath.Join(s.tmp, "out"), 0777); err != nil {
			base.Fatalf("go: cosmo fat build: %v", err)
		}
	}
	cmd := exec.Command(goCmd[0], append(slices.Clone(goCmd[1:]), s.traceArgs(rewriteOutputFlag(os.Args[1:], s.childO))...)...)
	cmd.Env = append(os.Environ(), "GOARCH="+s.arch, "GOCOSMOFAT_INNER=1")
	cmd.Env = append(cmd.Env, cosmoSiblingCgoEnv(s.arch)...)
	s.launch(cmd)
	return s
}

// cosmoSiblingCgoEnv is the C toolchain environment of a sibling build. The
// sibling compiles C for the other architecture, so it never inherits CC or
// CXX.
func cosmoSiblingCgoEnv(arch string) []string {
	cgo := "0"
	if cfg.BuildContext.CgoEnabled {
		cgo = "1"
	}
	return []string{
		"CGO_ENABLED=" + cgo,
		"CC=" + cfg.TargetCC("cosmo", arch),
		"CXX=" + cfg.TargetCXX("cosmo", arch),
	}
}

// cosmoFatten replaces each freshly built GOOS=cosmo executable (the Target
// of each main package in mains) with the assembled APE, merging in the
// sibling-architecture binary. That binary is produced by s (when there is
// one) using the linker's -apefat mode. By default the assembly also strips
// each embedded payload to its loadable span and writes the amd64 image's
// unstripped debug sidecar (<target>.dbg) next to the output. See
// cosmoMergeArgs.
func cosmoFatten(ctx context.Context, b *Builder, s *cosmoSibling, mains []*load.Package) {
	if s == nil && !cosmoAssembleEnabled() {
		return
	}
	if s != nil {
		defer s.cleanup()
		s.wait()
		s.importTrace()
	}
	lane := cosmoMergeLane(ctx)

	// Assembly re-reads the freshly written target and re-creates it with the merged APE.
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

	link := base.ToolCmd("link")
	for _, p := range regular {
		target := p.Target
		var sibling string
		if s != nil {
			sibling = s.childO
			if s.dir {
				sibling = filepath.Join(s.tmp, "out", filepath.Base(target))
			}
		}
		cosmoMerge(b, lane, link, p, target, sibling, "build")
	}
}

// cosmoMerge assembles one target's APE with the linker's -apefat mode, and
// reports a failure as fatal.
//
// The merge is a command this build issues, so -n and -x show it like every
// other one. Under -n it is only shown: the payloads it reads were printed
// rather than written. Running it would open a target that does not exist.
func cosmoMerge(b *Builder, lane trace.Lane, link []string, p *load.Package, target, sibling, what string) {
	args := cosmoMergeArgs(p, sibling)
	if cfg.BuildN {
		fmt.Fprintf(os.Stderr, "%s\n", joinUnambiguously(slices.Concat(link, args)))
		return
	}
	start := time.Now()
	id, err := cosmoMergeID(b, args, target, sibling)
	if err != nil {
		base.Fatalf("go: cosmo %s: assembling %s: %v", what, target, err)
	}
	if cosmoMergeRestore(b, id, target) {
		traceArgs := cosmoMergeTraceArgs(p, target, sibling, args, nil)
		traceArgs["cached"] = true
		lane.Since("cosmo fat merge", "cosmo", start, traceArgs)
		return
	}
	if cfg.BuildX {
		fmt.Fprintf(os.Stderr, "%s\n", joinUnambiguously(slices.Concat(link, args)))
	}
	merge := exec.Command(link[0], slices.Concat(link[1:], args)...)
	merge.Stdout = os.Stdout
	merge.Stderr = os.Stderr
	err = merge.Run()
	lane.Since("cosmo fat merge", "cosmo", start, cosmoMergeTraceArgs(p, target, sibling, args, err))
	if err != nil {
		base.Fatalf("go: cosmo %s: assembling %s: %v", what, target, err)
	}
	cosmoMergeStore(id, target)
}

// cosmoFatStartInstall kicks off the sibling-architecture install that
// cosmoFattenInstall will merge, and returns nil when fat builds are
// disabled. Call it immediately before the primary install, and pass
// hasMains=false when the command installs no main packages. Do this so no
// cross-architecture work is done for, say, "go install ./somelibrary".
func cosmoFatStartInstall(ctx context.Context, hasMains bool) *cosmoSibling {
	if !cosmoFatEnabled() || !hasMains {
		return nil
	}
	cosmoDebugMode() // reject invalid GOCOSMODEBUG before the sibling build

	s := &cosmoSibling{arch: cosmoFatArches[cfg.Goarch], what: "fat install"}
	goCmd := s.setup()
	s.traceOn(ctx) // after setup: the sibling's trace lives in its scratch dir

	cmd := exec.Command(goCmd[0], append(slices.Clone(goCmd[1:]), s.traceArgs(os.Args[1:])...)...)
	cmd.Env = append(os.Environ(),
		"GOOS=cosmo",
		"GOARCH="+s.arch,
		"GOCOSMOFAT_INNER=1",
		"GOPATH="+s.tmp,
		"GOMODCACHE="+cfg.GOMODCACHE,
	)
	cmd.Env = append(cmd.Env, cosmoSiblingCgoEnv(s.arch)...)
	s.launch(cmd)
	return s
}

// cosmoFattenInstall replaces each freshly installed GOOS=cosmo
// executable (the Target of each main package in mains) with the assembled
// APE, the go-install counterpart of cosmoFatten.
func cosmoFattenInstall(ctx context.Context, b *Builder, s *cosmoSibling, mains []*load.Package) {
	if s == nil && !cosmoAssembleEnabled() {
		return
	}
	if s != nil {
		defer s.cleanup()
		s.wait()
		s.importTrace()
	}
	if len(mains) == 0 {
		return
	}
	lane := cosmoMergeLane(ctx)

	link := base.ToolCmd("link")
	for _, p := range mains {
		target := p.Target
		var sibling string
		if s != nil {
			sibling = filepath.Join(s.tmp, "bin", "cosmo_"+s.arch, filepath.Base(target))
		}
		cosmoMerge(b, lane, link, p, target, sibling, "install")
	}
}

// traceOn puts this sibling build on its own trace row, named for the
// architecture it is producing. The row sorts after the build workers.
func (s *cosmoSibling) traceOn(ctx context.Context) {
	if !trace.Enabled(ctx) {
		return
	}
	s.ctx = ctx
	s.started = time.Now()
	s.lane = trace.LaneOf(trace.StartNamedGoroutine(ctx, "cosmo sibling ("+s.arch+")", cosmoSiblingLaneIndex))
	s.trace = filepath.Join(s.tmp, "trace.json")
}

// traceArgs returns the child's command line with -debug-trace pointed at
// the sibling's own file.
func (s *cosmoSibling) traceArgs(args []string) []string {
	if s.trace == "" {
		return args
	}
	return rewriteFlagValue(args, "-debug-trace", s.trace)
}

// importTrace folds the sibling's trace into the parent's, as its own process
// group. Called after the child has exited, so the file is complete.
func (s *cosmoSibling) importTrace() {
	if s == nil || s.trace == "" {
		return
	}
	f, err := os.Open(s.trace)
	if err != nil {
		// The child may have failed before writing anything, which the build already reported.
		return
	}
	defer f.Close()
	if err := trace.Import(s.ctx, f, cosmoSiblingPID, "go build (GOARCH="+s.arch+")"); err != nil {
		fmt.Fprintf(os.Stderr, "go: cosmo %s: reading sibling trace: %v\n", s.what, err)
	}
}

// cosmoSiblingPID is the process group the sibling build's rows appear under.
const cosmoSiblingPID = 1

// rewriteFlagValue returns args with flag's value replaced by value, in
// either the "-flag=v" or the "-flag v" spelling, appending "-flag value"
// when the flag is absent.
func rewriteFlagValue(args []string, flag, value string) []string {
	res := make([]string, 0, len(args)+2)
	replaced := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == flag:
			res = append(res, flag+"="+value)
			i++ // skip the original value
			replaced = true
		case strings.HasPrefix(a, flag+"="):
			res = append(res, flag+"="+value)
			replaced = true
		default:
			res = append(res, a)
		}
	}
	if !replaced {
		res = append(res, flag+"="+value)
	}
	return res
}

// cosmoMergeLane is the row the APE assembly is recorded on. The merge runs
// after the build workers are done, on the command's own goroutine. It gets
// a row of its own rather than borrowing a worker's.
func cosmoMergeLane(ctx context.Context) trace.Lane {
	if !trace.Enabled(ctx) {
		return trace.Lane{}
	}
	return trace.LaneOf(trace.StartNamedGoroutine(ctx, "cosmo fat merge", cosmoMergeLaneIndex))
}

// Rows sort by index, and these belong under the build workers, which
// take the indexes below them.
const (
	cosmoSiblingLaneIndex = 1000
	cosmoMergeLaneIndex   = 1001
)

// cosmoMergeTraceArgs describes one APE assembly: which binary was
// assembled, from which sibling payload, for which package and module, and
// what the linker was asked to do with the debug info.
func cosmoMergeTraceArgs(p *load.Package, target, sibling string, args []string, err error) map[string]any {
	out := map[string]any{
		"target":    target,
		"package":   p.ImportPath,
		"strip":     CosmoStrip(),
		"debug":     CosmoDebug(),
		"platforms": CosmoPlatforms(),
		"cmd":       strings.Join(args, " "),
	}
	if p.Module != nil {
		out["module"] = p.Module.Path
		if p.Module.Version != "" {
			out["module_version"] = p.Module.Version
		}
	}
	if sibling != "" {
		out["sibling"] = sibling
		if fi, statErr := os.Stat(sibling); statErr == nil {
			out["sibling_bytes"] = fi.Size()
		}
	}
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	if fi, statErr := os.Stat(target); statErr == nil {
		out["bytes"] = fi.Size()
	}
	return out
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
