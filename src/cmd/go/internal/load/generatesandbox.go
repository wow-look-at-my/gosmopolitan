// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"cmd/go/internal/cfg"
)

// sandboxArgv wraps a generate command in the host's sandbox.
//
// writable is the one directory the command may change. Everything else it can
// read stays read-only, and the caches a go command must write are named
// explicitly. A host with no backend at all is the only host that runs a
// directive unconfined, and it says so.
func sandboxArgv(writable string, argv ...string) ([]string, error) {
	switch hostSandboxOS() {
	case "linux":
		return bwrapArgv(writable, argv)
	case "darwin":
		return seatbeltArgv(writable, argv)
	}
	fmt.Fprintf(os.Stderr, "go: no sandbox on %s: running the generate directives of a dependency unconfined\n", runtime.GOOS)
	return argv, nil
}

// hostSandboxOS names the kernel this process runs on. A cosmo binary reports
// GOOS=cosmo everywhere, so the target name answers nothing about the host.
func hostSandboxOS() string {
	if runtime.GOOS != "cosmo" {
		return runtime.GOOS
	}
	if _, err := os.Stat("/System/Library/CoreServices"); err == nil {
		return "darwin"
	}
	if _, err := os.Stat("/proc/self"); err == nil {
		return "linux"
	}
	return runtime.GOOS
}

// bwrapArgv confines the command with bubblewrap.
func bwrapArgv(writable string, argv []string) ([]string, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bubblewrap is how a generate directive is confined on linux, and it is not installed: %w", err)
	}
	out := []string{
		bwrap,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--die-with-parent",
		"--bind", writable, writable,
	}
	// A generator writes temporary files, and so does the go command that runs
	// it. A tmpfs here would be tighter and would hide the caller's own TMPDIR.
	for _, dir := range append(writableCaches(), os.TempDir()) {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return nil, err
		}
		out = append(out, "--bind", dir, dir)
	}
	return append(out, argv...), nil
}

// seatbeltArgv confines the command with macOS's own sandbox.
func seatbeltArgv(writable string, argv []string) ([]string, error) {
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return nil, fmt.Errorf("sandbox-exec is how a generate directive is confined on darwin, and it is not there: %w", err)
	}
	var b strings.Builder
	b.WriteString("(version 1)(allow default)(deny file-write*)")
	for _, dir := range append([]string{writable}, writableCaches()...) {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, `(allow file-write* (subpath %q))`, dir)
	}
	// A generator writes temporary files, and every toolchain expects to.
	fmt.Fprintf(&b, `(allow file-write* (subpath %q))`, os.TempDir())
	return append([]string{sandboxExec, "-p", b.String()}, argv...), nil
}

// writableCaches names the directories a go command has to write to do its own
// work: the build cache and the module cache the generator's own dependencies
// land in.
func writableCaches() []string {
	var dirs []string
	for _, dir := range []string{cfg.GOMODCACHE, os.Getenv("GOCACHE")} {
		if dir != "" && filepath.IsAbs(dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}
