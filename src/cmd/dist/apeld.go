// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// The zig and LLD that make the bytes cmd/link's tests pin. ld64.lld writes
// its own version into the darwin loader.
const (
	apeldZigVersion = "0.16.0"
	apeldLLDVersion = "18.1.8"
)

const apeldLLVMInstall = "LLVM " + apeldLLDVersion + ": the llvm-18 and lld-18 packages from apt.llvm.org (then put /usr/lib/llvm-18/bin on PATH), " +
	"brew install llvm@18, or LLVM-" + apeldLLDVersion + "-win64.exe from https://github.com/llvm/llvm-project/releases"

// apeldTool is one program the loader build needs, and where to get it.
type apeldTool struct {
	name    string
	install string
}

var apeldTools = []apeldTool{
	{"zig", "zig " + apeldZigVersion + " from https://ziglang.org/download/"},
	{"ld64.lld", apeldLLVMInstall},
	{"llvm-strip", apeldLLVMInstall},
}

// apeldFlags go to every compile of a loader.
var apeldFlags = []string{"-Os", "-fno-stack-protector", "-fno-unwind-tables", "-fno-asynchronous-unwind-tables", "-Wall"}

// buildApeLoaders compiles the loaders that cmd/link embeds into every APE.
// It runs before anything compiles cmd/link, toolchain1 included, because
// the go:embed lines in apeloader.go read the files it writes.
func buildApeLoaders() {
	paths := map[string]string{}
	var missing []string
	for _, tool := range apeldTools {
		found, err := exec.LookPath(tool.name)
		if err != nil {
			missing = append(missing, "\t"+tool.name+": "+tool.install)
			continue
		}
		paths[tool.name] = found
	}
	if len(missing) > 0 {
		fatalf("the APE loaders cannot be built. These tools are not on PATH:\n%s\n", strings.Join(missing, "\n"))
	}
	zig, lld, strip := paths["zig"], paths["ld64.lld"], paths["llvm-strip"]

	if ver := strings.TrimSpace(run("", CheckExit, zig, "version")); ver != apeldZigVersion {
		fatalf("the APE loaders need zig %s, and %s is %s. Install it from https://ziglang.org/download/\n", apeldZigVersion, zig, ver)
	}
	if ver := run("", CheckExit, lld, "--version"); lldVersion(ver) != apeldLLDVersion {
		fatalf("the APE loaders need LLD %s, and %s says %s. Install %s\n", apeldLLDVersion, lld, strings.TrimSpace(ver), apeldLLVMInstall)
	}

	dir := pathf("%s/src/cmd/link/internal/ld/apeld", goroot)
	xmkdirall(pathf("%s/bin", dir))

	// The linker script packs each Linux loader into one PT_LOAD. A static
	// executable does not need its section headers, so the strip drops them.
	for _, target := range []struct{ triple, arch string }{{"x86_64-linux-musl", "amd64"}, {"aarch64-linux-musl", "arm64"}} {
		out := "bin/apeld-linux-" + target.arch
		args := []string{zig, "cc", "-target", target.triple}
		args = append(args, apeldFlags...)
		args = append(args, "-nostdlib", "-ffreestanding", "-static", "-fno-pic", "-fno-pie",
			"-Wl,--gc-sections", "-Wl,--build-id=none", "-Wl,-z,norelro", "-Wl,-T,linux/apeld.ld", "-s",
			"-o", out, "linux/apeld.c")
		run(dir, CheckExit, args...)
		run(dir, CheckExit, strip, "--strip-sections", out)
	}

	// zig compiles the darwin object and ld64.lld links it. zig's own Mach-O linker cannot merge __DATA_CONST into __DATA.
	libDir := zigLibDir(zig)
	if !isdir(pathf("%s/libc/darwin", libDir)) {
		fatalf("zig's darwin libc stubs are not in %s/libc/darwin\n", libDir)
	}
	obj := pathf("%s/apeld-darwin.o", workdir)
	// On a Mac, zig and ld64.lld would also read the host SDK and /usr/lib, and the loader would differ by host.
	args := []string{zig, "cc", "-target", "aarch64-macos"}
	args = append(args, apeldFlags...)
	args = append(args, "-nostdinc", "-isystem", pathf("%s/include", libDir), "-isystem", pathf("%s/libc/include/any-darwin-any", libDir),
		"-c", "-o", obj, "darwin/apeld.c")
	run(dir, CheckExit, args...)
	// The object goes before -lSystem. LLD lists imports in the order it first sees them, and a .tbd's order differs by host.
	run(dir, CheckExit, lld, "-arch", "arm64", "-platform_version", "macos", "12.0", "12.0",
		"-o", "bin/apeld-darwin-arm64", obj,
		"-Z", "-L"+pathf("%s/libc/darwin", libDir), "-lSystem",
		"-dead_strip", "-S", "-x", "-no_uuid", "-no_function_starts", "-no_data_const", "-fixup_chains")
	xremove(obj)
}

// lldVersion returns the word after "LLD" in the output of ld64.lld --version,
// such as "18.1.8" from "Homebrew LLD 18.1.8 (compatible with GNU linkers)".
func lldVersion(out string) string {
	words := strings.Fields(out)
	for idx := 0; idx+1 < len(words); idx++ {
		if words[idx] == "LLD" {
			return words[idx+1]
		}
	}
	return ""
}

func zigLibDir(zig string) string {
	env := run("", CheckExit, zig, "env")
	dir, err := parseZigLibDir(env)
	if err != nil {
		fatalf("%v\n", err)
	}
	return dir
}

func parseZigLibDir(env string) (string, error) {
	for _, line := range strings.Split(env, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), ".lib_dir = ")
		if !ok {
			continue
		}
		dir, err := strconv.Unquote(strings.TrimSuffix(value, ","))
		if err != nil {
			return "", fmt.Errorf("cannot read lib_dir in zig env: %q: %v", line, err)
		}
		return dir, nil
	}
	return "", fmt.Errorf("zig env names no lib_dir:\n%s", env)
}
