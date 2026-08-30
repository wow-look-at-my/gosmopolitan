// checkLookPath: exec.LookPath and exec.Command name resolution
// against the HOST-format PATH. This is the regression gate for the
// NT lookup bug (2026-07-20): a cosmo binary on a Windows host sees
// the raw ';'-separated, drive-lettered, backslashed PATH value from
// the NT environment block (typically under the name "Path", not
// "PATH"), and the on-disk executables carry PATHEXT suffixes - the
// unix lookup (':' split, no suffix probing, exact-case Getenv) can
// never resolve anything there, so exec.LookPath("go") failed on
// every NT host while linux/macOS worked.
//
// The check replicates the failing shape end to end: it copies this
// probe binary into a fresh directory as a uniquely-named dummy
// (suffixed ".exe" on NT), appends that directory to PATH in the
// host's own format (';' + "C:\..." on NT when TEMP is drive-shaped;
// ':' + "/..." elsewhere), resolves the bare name via exec.LookPath
// AND via exec.Command, and actually runs the resolved path. Where a
// "go" binary is genuinely present on PATH (verified by an
// independent raw os.Stat scan, not by the machinery under test), it
// additionally requires exec.LookPath("go") to succeed - the exact
// repro from the field report - and skips that part gracefully
// otherwise so no leg can false-fail.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// envFold returns the value and exact key of the first
// case-insensitive match for name in the environment. cosmo's
// os.Getenv is an exact-match scan (unix semantics), but NT
// environment blocks typically spell "Path"/"TEMP" in mixed or upper
// case, so probe code that must work on NT matches like the OS API
// does.
func envFold(name string) (key, value string, found bool) {
	for _, kv := range os.Environ() {
		if eq := strings.IndexByte(kv, '='); eq > 0 && strings.EqualFold(kv[:eq], name) {
			key, value, found = kv[:eq], kv[eq+1:], true
		}
	}
	return key, value, found
}

// copySelf copies this binary to dst (0o755) and reports whether the
// copy can be executed directly (same magic logic as selfCommand: an
// assimilated ELF/Mach-O runs directly, a pristine APE needs the
// shell bootstrap except on NT, where the MZ header IS a valid PE).
func copySelf(name, dst string, onNT bool) (direct, bad bool) {
	self, err := os.Executable()
	if err != nil {
		fail(name, "os.Executable: %v", err)
		return false, true
	}
	src, err := os.Open(self)
	if err != nil {
		fail(name, "open %q: %v", self, err)
		return false, true
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		fail(name, "create %q: %v", dst, err)
		return false, true
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		fail(name, "copy %q -> %q: %v", self, dst, err)
		return false, true
	}
	if err := out.Close(); err != nil {
		fail(name, "close %q: %v", dst, err)
		return false, true
	}
	var magic [4]byte
	f, err := os.Open(dst)
	if err != nil {
		fail(name, "reopen %q: %v", dst, err)
		return false, true
	}
	_, err = io.ReadFull(f, magic[:])
	f.Close()
	if err != nil {
		fail(name, "read magic of %q: %v", dst, err)
		return false, true
	}
	direct = (magic == [4]byte{0x7f, 'E', 'L', 'F'}) ||
		(magic == [4]byte{0xcf, 0xfa, 0xed, 0xfe}) ||
		(magic == [4]byte{0xca, 0xfe, 0xba, 0xbe}) ||
		onNT
	return direct, false
}

// rawPathHas scans a PATH value by hand - strings and os.Stat only,
// independent of the LookPath machinery under test - and reports an
// executable candidate for name. Used as the oracle for the
// host-binary half of the check.
func rawPathHas(pathVal, name string, onNT bool) (string, bool) {
	sep, exts := ":", []string{""}
	if onNT {
		sep, exts = ";", []string{".exe", ".com", ".bat", ".cmd"}
	}
	for _, dir := range strings.Split(pathVal, sep) {
		dir = strings.ReplaceAll(dir, `"`, ``)
		if dir == "" {
			continue
		}
		slash := "/"
		if onNT && !strings.HasSuffix(dir, "/") && !strings.HasSuffix(dir, `\`) {
			slash = `\`
		}
		for _, e := range exts {
			cand := dir + slash + name + e
			fi, err := os.Stat(cand)
			if err != nil || fi.IsDir() {
				continue
			}
			if !onNT && fi.Mode()&0o111 == 0 {
				continue
			}
			return cand, true
		}
	}
	return "", false
}

func checkLookPath() {
	onNT := os.Getenv("OS") == "Windows_NT"

	// A fresh directory for the dummy, in the host's native path
	// format. On NT prefer a genuinely Windows-shaped directory
	// (drive letter + backslashes, like the real C:\hostedtoolcache
	// entries) built from TEMP/TMP; fall back to os.MkdirTemp (whose
	// cosmo spelling "/tmp/..." still proves the ';' split and suffix
	// probing).
	var dir string
	dirSep := "/"
	if onNT {
		_, base, _ := envFold("TEMP")
		if base == "" {
			_, base, _ = envFold("TMP")
		}
		base = strings.TrimRight(base, `\/`)
		if len(base) >= 2 && base[1] == ':' {
			dir = fmt.Sprintf(`%s\rp-lookpath-%d`, base, os.Getpid())
			if err := os.Mkdir(dir, 0o755); err != nil {
				fail("lookpath", "mkdir %q: %v", dir, err)
				return
			}
			dirSep = `\`
		}
	}
	if dir == "" {
		d, err := os.MkdirTemp("", "rp-lookpath")
		if err != nil {
			fail("lookpath", "MkdirTemp: %v", err)
			return
		}
		dir = d
	}
	defer os.RemoveAll(dir)

	// The dummy executable: a uniquely-named copy of this probe
	// binary (so nothing else on PATH can shadow or satisfy it),
	// ".exe"-suffixed on NT so resolving the BARE name proves the
	// PATHEXT probing.
	base := fmt.Sprintf("rp-dummy-%d", os.Getpid())
	dummy := base
	if onNT {
		dummy = base + ".exe"
	}
	direct, bad := copySelf("lookpath", dir+dirSep+dummy, onNT)
	if bad {
		return
	}

	// Append dir to PATH in host format, via the environment name the
	// process actually has (NT blocks usually spell it "Path"; an
	// exact-case os.Setenv("PATH", ...) would leave the original
	// "Path" value in place for a case-folding consumer to find
	// first).
	pathKey, pathVal, found := envFold("PATH")
	if !found {
		pathKey = "PATH"
	}
	pathSep := ":"
	if onNT {
		pathSep = ";"
	}

	// os.PathListSeparator is a variable under cosmo, resolved from the host
	// at startup. onNT above reads the NT environment block, so it is an
	// independent oracle: a build that compiled the unix colon in disagrees
	// here on a Windows host. filepath.SplitList is the consumer that breaks
	// when it does - it is how anything reads PATH without exec.LookPath.
	if got := string(os.PathListSeparator); got != pathSep {
		fail("lookpath", "os.PathListSeparator = %q, host wants %q", got, pathSep)
		return
	}
	if parts := filepath.SplitList("a" + pathSep + "b"); len(parts) != 2 || parts[0] != "a" || parts[1] != "b" {
		fail("lookpath", "filepath.SplitList(%q) = %q, want [a b]", "a"+pathSep+"b", parts)
		return
	}
	// An NT entry may be quoted, and the quotes may wrap the separator itself.
	if onNT {
		quoted := `"a;b"` + pathSep + "c"
		if parts := filepath.SplitList(quoted); len(parts) != 2 || parts[0] != "a;b" || parts[1] != "c" {
			fail("lookpath", "filepath.SplitList(%q) = %q, want [a;b c]", quoted, parts)
			return
		}
	}

	newPath := dir
	if pathVal != "" {
		newPath = pathVal + pathSep + dir
	}
	if err := os.Setenv(pathKey, newPath); err != nil {
		fail("lookpath", "Setenv(%s): %v", pathKey, err)
		return
	}
	defer os.Setenv(pathKey, pathVal)

	// The core assertion: resolve the bare name.
	got, err := exec.LookPath(base)
	if err != nil {
		fail("lookpath", "LookPath(%q) with %q on %s-separated PATH: %v", base, dir, pathSep, err)
		return
	}
	if !strings.HasSuffix(got, dummy) {
		fail("lookpath", "LookPath(%q) = %q, want suffix %q", base, got, dummy)
		return
	}
	if fi, err := os.Stat(got); err != nil || fi.IsDir() {
		fail("lookpath", "LookPath result %q does not stat as a file: %v", got, err)
		return
	}

	// exec.Command must resolve the bare name through the same lookup.
	if c := exec.Command(base); c.Err != nil || c.Path == base {
		fail("lookpath", "exec.Command(%q): Err=%v Path=%q, want a resolved path", base, c.Err, c.Path)
		return
	}

	// And the resolved path must actually run (RUNTIMEPROBE_CHILD=1
	// child mode, like checkExec).
	var cmd *exec.Cmd
	if direct {
		cmd = exec.Command(got)
	} else {
		cmd = exec.Command("/bin/sh", got)
	}
	cmd.Env = append(os.Environ(), "RUNTIMEPROBE_CHILD=1")
	var stdout, stderrBuf strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		fail("lookpath", "start %q (direct=%v): %v", got, direct, err)
		return
	}
	err2, completed := waitBounded("lookpath", cmd)
	if !completed {
		return
	}
	if err2 != nil {
		fail("lookpath", "run %q (direct=%v): %v (stderr: %q)", got, direct, err2, stderrBuf.String())
		return
	}
	if !strings.HasPrefix(stdout.String(), "child-ok") {
		fail("lookpath", "child output %q, want child-ok prefix", stdout.String())
		return
	}

	// Host-binary half: where a real "go" is on the (original) PATH -
	// per the independent raw scan - LookPath must find it. This is
	// the exact shape from the field report (exec.LookPath("go")
	// after setup-go). Skipped, not failed, when absent.
	hostNote := "hostlookup=skipped"
	if cand, present := rawPathHas(pathVal, "go", onNT); present {
		hostGot, err := exec.LookPath("go")
		if err != nil {
			fail("lookpath", "LookPath(go) failed although %q exists: %v", cand, err)
			return
		}
		if fi, err := os.Stat(hostGot); err != nil || fi.IsDir() {
			fail("lookpath", "LookPath(go) = %q, does not stat as a file: %v", hostGot, err)
			return
		}
		hostNote = "hostlookup=" + hostGot
	}

	ok("lookpath", fmt.Sprintf("%s (%s)", got, hostNote))
}
