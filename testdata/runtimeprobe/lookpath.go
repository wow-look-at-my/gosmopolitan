// exec.LookPath check (lookpath): plant a canary executable in a fresh
// directory, point PATH at that directory, and require LookPath to
// resolve the bare name to the planted file. One binary, host-dependent
// PATH semantics:
//
// On unix hosts the planted name is extensionless, the entry is the
// unix-shaped directory, and the list separator is ":" - the classic
// lp_unix rules (execute bit required).
//
// On Windows NT hosts the check reproduces the exact consumer failure
// that motivated it (go-toolchain's smoke-windows step, run
// 29738066073): a real NT PATH is ";"-separated with drive-shaped
// entries ("C:\hostedtoolcache\...\bin"), and executables carry a
// PATHEXT extension. So the canary is "<name>.exe", the PATH entry is
// the drive-shaped spelling of the temp directory, and LookPath must
// find the canary from the bare name. A LookPath that applies unix
// rules to that environment splits "C:\..." at the drive colon and
// probes for an extensionless name - it can never succeed, and every
// consumer that locates its tools via PATH (e.g. go-toolchain finding
// the host go) breaks on NT while working on the same runner's
// pre-APE native windows payload.
//
// The temp directory's drive-shaped spelling is recovered with the
// same Chdir/Getwd round-trip idiom checkWd uses for alias
// resolution: os.MkdirTemp returns the unix-shaped "/tmp/..." alias,
// Getwd returns the real "/c/users/.../temp/..." spelling, and the
// "/c/..." form maps 1:1 onto "C:\...".
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ntDriveShape converts the cosmo path layer's rooted drive form
// ("/c/users/x") into the native NT spelling ("C:\users\x"). Paths in
// any other shape are returned unchanged.
func ntDriveShape(p string) string {
	if len(p) >= 2 && p[0] == '/' &&
		('a' <= p[1] && p[1] <= 'z' || 'A' <= p[1] && p[1] <= 'Z') &&
		(len(p) == 2 || p[2] == '/') {
		rest := strings.ReplaceAll(p[2:], "/", `\`)
		if rest == "" {
			rest = `\`
		}
		return strings.ToUpper(p[1:2]) + ":" + rest
	}
	return p
}

// lookPathDir returns the PATH entry that should make the canary in
// dir resolvable: the directory itself on unix hosts, its drive-shaped
// real spelling on NT.
func lookPathDir(dir string) (string, error) {
	if !probeHostIsNT() {
		return dir, nil
	}
	wd0, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		return "", fmt.Errorf("Chdir(%q): %v", dir, err)
	}
	real, err := os.Getwd()
	// Restore the working directory before acting on any error: later
	// checks (files, wd round-trip) depend on it.
	if cderr := os.Chdir(wd0); cderr != nil {
		return "", fmt.Errorf("restoring wd to %q: %v", wd0, cderr)
	}
	if err != nil {
		return "", fmt.Errorf("Getwd in %q: %v", dir, err)
	}
	return ntDriveShape(real), nil
}

func checkLookPath() {
	dir, err := os.MkdirTemp("", "lookpath")
	if err != nil {
		fail("lookpath", "MkdirTemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	const name = "lpcanary"
	file := name
	if probeHostIsNT() {
		file += ".exe"
	}
	canary := filepath.Join(dir, file)
	if err := os.WriteFile(canary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		fail("lookpath", "planting %q: %v", canary, err)
		return
	}

	entry, err := lookPathDir(dir)
	if err != nil {
		fail("lookpath", "%v", err)
		return
	}

	oldPath, hadPath := os.LookupEnv("PATH")
	if err := os.Setenv("PATH", entry); err != nil {
		fail("lookpath", "Setenv(PATH, %q): %v", entry, err)
		return
	}
	got, lookErr := exec.LookPath(name)
	// Restore PATH before judging the result: later checks spawn
	// children that inherit the environment.
	if hadPath {
		os.Setenv("PATH", oldPath)
	} else {
		os.Unsetenv("PATH")
	}

	if lookErr != nil {
		fail("lookpath", "exec.LookPath(%q) with PATH=%q: %v (canary at %q)", name, entry, lookErr, canary)
		return
	}
	gotInfo, err := os.Stat(got)
	if err != nil {
		fail("lookpath", "Stat(%q) on LookPath result: %v", got, err)
		return
	}
	wantInfo, err := os.Stat(canary)
	if err != nil {
		fail("lookpath", "Stat(%q) on planted canary: %v", canary, err)
		return
	}
	if !os.SameFile(gotInfo, wantInfo) {
		fail("lookpath", "LookPath(%q) = %q, which is not the planted %q", name, got, canary)
		return
	}
	ok("lookpath", got)
}
