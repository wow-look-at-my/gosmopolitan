// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRunScratch pins which paths the test cache still refuses to hash. Only the
// temporary directory qualifies. Every other path a test reads is an input to
// that test. Dropping the ones outside the module root is what let a changed
// file replay a stale pass. A path that no longer resolves still has to answer,
// because a test may read a file and then remove it, and it has. To answer in
// the spelling the test used. A test binary opens itself under the go command's
// per-run build directory, which is gone by the next run and, on macOS, spelled.
// Through a symlink.
func TestRunScratch(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", os.TempDir(), err)
	}
	sep := string(filepath.Separator)

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"a file the run created under the temporary directory", filepath.Join(tmp, "TestFoo123", "fixture.txt"), true},
		{"the temporary directory itself", tmp, true},
		{"a removed path under the temporary directory", filepath.Join(tmp, "gone-9d3f", "gone.txt"), true},
		{"a removed path under the temporary directory as spelled", filepath.Join(os.TempDir(), "go-build1234", "b002", "logger.test"), true},
		{"a system file", filepath.Join(sep, "etc", "hosts"), false},
		{"a file in another checkout", filepath.Join(sep, "srv", "shared", "config.yaml"), false},
		{"a removed path elsewhere", filepath.Join(sep, "srv", "gone-9d3f.txt"), false},
	}
	for _, c := range cases {
		if got := isRunScratch(c.path); got != c.want {
			t.Errorf("isRunScratch(%q) = %v, want %v: %s", c.path, got, c.want, c.name)
		}
	}
}

// TestProcessLocal pins the paths that name the process reading them. The go
// command reading one learns about itself, never about the test.
func TestProcessLocal(t *testing.T) {
	for path, want := range map[string]bool{
		"/proc/self/cmdline":         true,
		"/proc/self":                 true,
		"/proc/thread-self/status":   true,
		"/dev/fd":                    true,
		"/dev/fd/11":                 true,
		"/dev/stdin":                 true,
		"/proc/1/cmdline":            true,
		"/proc/25826":                true,
		"/proc/25826/stat":           true,
		"/proc/stat":                 true,
		"/proc/meminfo":              true,
		"/dev/shm":                   true,
		"/dev/shm/go-ipc-x-1.name":   true,
		"/dev/shmem":                 false,
		"/proc/selfish":              false,
		"/proc/cpuinfo":              false,
		"/dev/fdisk":                 false,
		"/etc/hosts":                 false,
		"/proc/sys/kernel/osrelease": false,
	} {
		if got := isProcessLocal(path); got != want {
			t.Errorf("isProcessLocal(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestProcessLocalIPCDir pins go-ipc's runtime directory as process-local. On
// Windows and macOS it holds the life sockets of whichever processes run now,
// so a test that lists it never hit the cache.
func TestProcessLocalIPCDir(t *testing.T) {
	t.Serial() // it replaces ipcRuntimeDir
	// A host with no HOME, such as wasip1, has no user cache directory, and then no directory is go-ipc's.
	wantDir := ""
	if userCache, err := os.UserCacheDir(); err == nil {
		wantDir = filepath.Join(userCache, "gosmopolitan", "go-ipc")
	}
	if got := ipcRuntimeDir(); got != wantDir {
		t.Errorf("ipcRuntimeDir() = %q, want %q", got, wantDir)
	}

	base := t.TempDir()
	dir := filepath.Join(base, "gosmopolitan", "go-ipc")
	saved := ipcRuntimeDir
	ipcRuntimeDir = func() string { return dir }
	defer func() { ipcRuntimeDir = saved }()
	for path, want := range map[string]bool{
		dir:                                   true,
		filepath.Join(dir, "go-ipc-x-1.life"): true,
		filepath.Join(base, "gosmopolitan"):   false,
		dir + "x":                             false,
	} {
		if got := isProcessLocal(path); got != want {
			t.Errorf("isProcessLocal(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestWindowsLiveState pins the Windows paths the system rewrites as it runs.
// A drive letter is a volume only on Windows, so the paging files answer there.
func TestWindowsLiveState(t *testing.T) {
	onWindows := runtime.GOOS == "windows"
	for path, want := range map[string]bool{
		`\\.\pipe\`:             true,
		`\\.\pipe\go-test-1234`: true,
		`\\.\NUL`:               true,
		`\\?\Volume{2f14af21-6683-4c83-b3a4-67a0cc7ecac5}\a\_temp`: true,
		`\\?\volume{2f14af21-6683-4c83-b3a4-67a0cc7ecac5}\`:        true,
		`c:\pagefile.sys`:      onWindows,
		`C:\PAGEFILE.SYS`:      onWindows,
		`d:\swapfile.sys`:      onWindows,
		`c:\hiberfil.sys`:      onWindows,
		`c:\data\pagefile.sys`: false,
		`c:\Windows\win.ini`:   false,
		`\\server\share\a.sys`: false,
		"/etc/hosts":           false,
	} {
		if got := isWindowsLiveState(path); got != want {
			t.Errorf("isWindowsLiveState(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestSystemListing pins the directories whose listing the test cache does not
// hash: the Windows directory and its System32, which the system rewrites.
func TestSystemListing(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "Windows")
	t.Setenv("SystemRoot", root)
	for path, want := range map[string]bool{
		root:                            true,
		filepath.Join(root, "System32"): true,
		filepath.Join(root, "system32"): true,
		filepath.Join(root, "System32", "drivers"): false,
		filepath.Join(root, "Fonts"):               false,
	} {
		if got := isSystemListing(path); got != want {
			t.Errorf("isSystemListing(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestVolumeRoot pins the spellings of a Windows volume root, whose listing the
// test cache does not hash.
func TestVolumeRoot(t *testing.T) {
	onWindows := runtime.GOOS == "windows"
	for path, want := range map[string]bool{
		`C:\`:         onWindows,
		`D:\`:         onWindows,
		`\\?\C:\`:     onWindows,
		`c:`:          onWindows,
		`D:\a\_temp`:  false,
		`\\?\C:\Go`:   false,
		`\\srv\share`: false,
		"/":           false,
	} {
		if got := isVolumeRoot(path); got != want {
			t.Errorf("isVolumeRoot(%q) = %v, want %v", path, got, want)
		}
	}
}
