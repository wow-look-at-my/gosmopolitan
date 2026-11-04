// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package ld

import _ "embed"

import (
	"internal/buildcfg"
	"internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cmd/internal/objabi"
)

// TestAPECosmoNosplitBudget pins the nosplit budget: cosmo gets the same
// extra stack guard unit as AIX and OpenBSD. This is because its darwin-host
// syscall emulation is a deep nosplit chain over dlsym'd libc.
func TestAPECosmoNosplitBudget(t *testing.T) {
	oldGOOS := buildcfg.GOOS
	defer func() { buildcfg.GOOS = oldGOOS }()

	buildcfg.GOOS = "linux"
	if got := objabi.StackNosplit(false); got != 800 {
		t.Errorf("StackNosplit(false) for linux = %d, want 800", got)
	}
	buildcfg.GOOS = "cosmo"
	if got := objabi.StackNosplit(false); got != 1600 {
		t.Errorf("StackNosplit(false) for cosmo = %d, want 1600", got)
	}
}

// TestAPECosmoNosplitABI0SyscallChain builds a GOOS=cosmo fat APE whose
// assembly enters syscall.Syscall/Syscall6/RawSyscall through ABI0. The
// way golang.org/x/sys/unix's asm_linux_*.s files do. The ABI0 entry
// drags the ABI-bridge wrappers into the nosplit chain on top of the
// darwin syscall-emulation spine (syscall6SlowDarwin and the darwin*
// helpers). This is the deepest nosplit chain a cosmo binary links.
// Under a single stack guard unit the arm64 link fails with "nosplit
// stack over byte limit". Any module that imports x/sys/unix hits it,
// since cosmo satisfies the linux build tag.
func TestAPECosmoNosplitABI0SyscallChain(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	if testing.Short() {
		t.Skip("builds cosmo std for two architectures in short mode")
	}

	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module nosplitprobe\n\ngo 1.26\n")
	write("main.go", `package main

import "syscall"

func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)
func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)
func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

func main() {
	pid, _, _ := Syscall(39, 0, 0, 0)
	r, _, _ := Syscall6(39, 0, 0, 0, 0, 0, 0)
	rr, _, _ := RawSyscall(39, 0, 0, 0)
	_ = pid + r + rr
}
`)
	write("sys_arm64.s", testAPECosmoNosplitABI0SyscallChainC)
	write("sys_amd64.s", testAPECosmoNosplitABI0SyscallChainC2)

	// The fat build links both architectures; arm64 has the deeper frames and is where the budget overflowed.
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", filepath.Join(dir, "probe.com"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=cosmo", "GOCOSMOFAT=", "GOCOSMOSTRIP=", "GOCOSMODEBUG=", "GOCOSMOPLATFORMS=")
	if msg, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(msg), "nosplit stack over") {
			t.Fatalf("nosplit budget regressed: the ABI0 syscall chain no longer fits.\n"+
				"Check the cosmo stack guard unit in cmd/internal/objabi/stack.go and\n"+
				"internal/runtime/sys/consts.go before shaving frames.\n%v\n%s", err, msg)
		}
		t.Fatalf("go build: %v\n%s", err, msg)
	}
}

//go:embed testdata/test-ape-cosmo-nosplit-abi0-syscall-chain.c
var testAPECosmoNosplitABI0SyscallChainC string

//go:embed testdata/test-ape-cosmo-nosplit-abi0-syscall-chain-2.c
var testAPECosmoNosplitABI0SyscallChainC2 string
