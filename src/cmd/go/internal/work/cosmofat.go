// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
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

// cosmoFatten replaces each freshly built GOOS=cosmo executable in targets
// with a fat (amd64+arm64) APE carrying a native Windows amd64 PE payload.
// It reruns the original go build command for the sibling cosmo architecture
// and for windows/amd64 into temporary locations, then merges each group of
// binaries with the linker's -apefat mode. Pass dir=true when targets were
// written to a -o directory, so the sibling builds also use one.
func cosmoFatten(targets []string, dir bool) {
	if !cosmoFatEnabled() || len(targets) == 0 {
		return
	}
	// Fattening re-reads the freshly written target and re-creates it with
	// the merged APE. That only makes sense for regular files: with
	// -o /dev/null (or any other special file) the target reads back empty
	// and cannot be replaced, so leave the primary build's output as-is.
	regular := make([]string, 0, len(targets))
	for _, target := range targets {
		if fi, err := os.Stat(target); err == nil && !fi.Mode().IsRegular() {
			continue
		}
		regular = append(regular, target)
	}
	targets = regular
	if len(targets) == 0 {
		return
	}
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

	winO := filepath.Join(tmp, "windows.exe")
	if dir {
		winO = filepath.Join(tmp, "windows") + string(os.PathSeparator)
		if err := os.Mkdir(filepath.Join(tmp, "windows"), 0777); err != nil {
			base.Fatalf("go: cosmo fat build: %v", err)
		}
	}
	winArgs := rewriteOutputFlag(os.Args[1:], winO)
	winCmd := exec.Command(goCmd, winArgs...)
	winCmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64")
	winCmd.Stdout = os.Stdout
	winCmd.Stderr = os.Stderr
	if err := winCmd.Run(); err != nil {
		base.Fatalf("go: cosmo fat build: GOOS=windows GOARCH=amd64 build failed: %v\n(set GOCOSMOFAT=0 for a cosmo-only binary)", err)
	}

	link := base.Tool("link")
	for _, target := range targets {
		sibling := childO
		win := winO
		if dir {
			sibling = filepath.Join(tmp, "out", filepath.Base(target))
			win = windowsDirTarget(tmp, target)
		}
		merge := exec.Command(link, "-apefat", target+","+sibling+","+win, "-o", target)
		merge.Stdout = os.Stdout
		merge.Stderr = os.Stderr
		if err := merge.Run(); err != nil {
			base.Fatalf("go: cosmo fat build: merging %s: %v", target, err)
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

func windowsDirTarget(tmp, target string) string {
	baseName := filepath.Base(target)
	win := filepath.Join(tmp, "windows", baseName)
	if _, err := os.Stat(win); err == nil {
		return win
	}
	if filepath.Ext(baseName) != ".exe" {
		winExe := win + ".exe"
		if _, err := os.Stat(winExe); err == nil {
			return winExe
		}
	}
	return win
}
