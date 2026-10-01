// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syscall_test

import (
	"internal/runtime/syscall/cosmo"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// Package syscall's own tests cannot import testing (os imports syscall),
// so these run from outside and reach the conversions through
// export_cosmo_test.go.

const (
	appleMNT_RDONLY      = syscall.AppleMntRdonlyForTest
	appleMNT_SYNCHRONOUS = syscall.AppleMntSyncForTest
	appleMNT_NOEXEC      = syscall.AppleMntNoexecForTest
	appleMNT_NOSUID      = syscall.AppleMntNosuidForTest
	appleMNT_NODEV       = syscall.AppleMntNodevForTest
	appleMNT_NOATIME     = syscall.AppleMntNoatimeForTest

	linuxST_RDONLY      = syscall.LinuxStRdonlyForTest
	linuxST_NOSUID      = syscall.LinuxStNosuidForTest
	linuxST_NODEV       = syscall.LinuxStNodevForTest
	linuxST_NOEXEC      = syscall.LinuxStNoexecForTest
	linuxST_SYNCHRONOUS = syscall.LinuxStSyncForTest
	linuxST_NOATIME     = syscall.LinuxStNoatimeForTest
)

var (
	darwinStatfsToLinux   = syscall.DarwinStatfsToLinuxForTest
	darwinMntFlagsToLinux = syscall.DarwinMntFlagsToLinuxForTest
	darwinUtsnameToLinux  = syscall.DarwinUtsnameToLinuxForTest
)

type (
	Statfs_t = syscall.Statfs_t
	Utsname  = syscall.Utsname
)

// The statfs and utsname conversions (bigbuf_cosmo.go) are pure struct
// rewriting, so they are pinned here on any host rather than only where
// a macOS runner can reach them.

func TestDarwinStatfsToLinux(t *testing.T) {
	src := cosmo.DarwinStatfs{
		Bsize:  4096,
		Iosize: 1 << 20, // optimal transfer size: no Linux counterpart
		Blocks: 1000,
		Bfree:  400,
		Bavail: 300,
		Files:  50,
		Ffree:  20,
		Fsid:   [2]int32{7, 9},
		Type:   26, // Apple's own filesystem-type number for APFS
		Flags:  appleMNT_RDONLY | appleMNT_NOSUID | appleMNT_NOATIME,
	}
	var dst Statfs_t
	darwinStatfsToLinux(&dst, &src)

	if dst.Bsize != 4096 || dst.Frsize != 4096 {
		t.Errorf("Bsize/Frsize = %d/%d, want 4096/4096", dst.Bsize, dst.Frsize)
	}
	if dst.Blocks != 1000 || dst.Bfree != 400 || dst.Bavail != 300 {
		t.Errorf("block counts = %d/%d/%d, want 1000/400/300", dst.Blocks, dst.Bfree, dst.Bavail)
	}
	if dst.Files != 50 || dst.Ffree != 20 {
		t.Errorf("inode counts = %d/%d, want 50/20", dst.Files, dst.Ffree)
	}
	if dst.Fsid.X__val != [2]int32{7, 9} {
		t.Errorf("Fsid = %v, want [7 9]", dst.Fsid.X__val)
	}
	if dst.Type != 26 {
		t.Errorf("Type = %d, want Apple's 26 passed through", dst.Type)
	}
	want := int64(linuxST_RDONLY | linuxST_NOSUID | linuxST_NOATIME)
	if dst.Flags != want {
		t.Errorf("Flags = %#x, want %#x", dst.Flags, want)
	}
	// Namelen has no Apple source and is deliberately left alone rather
	// than filled with a guess.
	if dst.Namelen != 0 {
		t.Errorf("Namelen = %d, want 0 (Apple statfs has no such field)", dst.Namelen)
	}

	// Every conversion starts from a cleared destination, so a reused
	// buffer cannot leak a previous filesystem's numbers.
	dst = Statfs_t{Namelen: 255, Spare: [4]int64{1, 2, 3, 4}}
	darwinStatfsToLinux(&dst, &cosmo.DarwinStatfs{})
	if dst.Namelen != 0 || dst.Spare != [4]int64{} {
		t.Errorf("stale fields survived: Namelen=%d Spare=%v", dst.Namelen, dst.Spare)
	}
}

func TestDarwinMntFlagsToLinux(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apple uint32
		linux int64
	}{
		{"none", 0, 0},
		{"rdonly", appleMNT_RDONLY, linuxST_RDONLY},
		{"nosuid", appleMNT_NOSUID, linuxST_NOSUID},
		{"nodev", appleMNT_NODEV, linuxST_NODEV},
		{"noexec", appleMNT_NOEXEC, linuxST_NOEXEC},
		{"sync", appleMNT_SYNCHRONOUS, linuxST_SYNCHRONOUS},
		{"noatime", appleMNT_NOATIME, linuxST_NOATIME},
		// An Apple-only flag (MNT_LOCAL, 0x1000) reports nothing rather
		// than colliding with an unrelated Linux bit.
		{"apple-only", 0x1000, 0},
	} {
		if got := darwinMntFlagsToLinux(tc.apple); got != tc.linux {
			t.Errorf("%s: got %#x, want %#x", tc.name, got, tc.linux)
		}
	}
}

// TestRawStatfsLinuxBuffer issues statfs and fstatfs the way
// golang.org/x/sys/unix does: straight through Syscall and Syscall6, with
// a Linux Statfs_t and nothing in a3. On a macOS host that buffer is not
// the shape Apple's statfs fills, so this is the call the conversion in
// Syscall exists for. A disk-pressure check that reads free space
// through x/sys sees these numbers or an error.
func TestRawStatfsLinuxBuffer(t *testing.T) {
	dir := t.TempDir()
	path, err := syscall.BytePtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}

	var viaPath Statfs_t
	if _, _, e := syscall.Syscall(syscall.SYS_STATFS, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&viaPath)), 0); e != 0 {
		t.Fatalf("Syscall(SYS_STATFS) with a Linux Statfs_t: %v", e)
	}
	checkStatfs(t, "SYS_STATFS", &viaPath)

	var viaPath6 Statfs_t
	if _, _, e := syscall.Syscall6(syscall.SYS_STATFS, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&viaPath6)), 0, 0, 0, 0); e != 0 {
		t.Fatalf("Syscall6(SYS_STATFS) with a Linux Statfs_t: %v", e)
	}
	checkStatfs(t, "SYS_STATFS via Syscall6", &viaPath6)

	dirFile, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer dirFile.Close()
	var viaFd Statfs_t
	if _, _, e := syscall.Syscall(syscall.SYS_FSTATFS, dirFile.Fd(), uintptr(unsafe.Pointer(&viaFd)), 0); e != 0 {
		t.Fatalf("Syscall(SYS_FSTATFS) with a Linux Statfs_t: %v", e)
	}
	checkStatfs(t, "SYS_FSTATFS", &viaFd)

	// One filesystem, asked by path and by descriptor: its identity and
	// its size agree. Free counts move under other writers, so they are
	// left out.
	if viaFd.Fsid != viaPath.Fsid || viaFd.Bsize != viaPath.Bsize || viaFd.Blocks != viaPath.Blocks {
		t.Errorf("fstatfs fsid=%v bsize=%d blocks=%d, statfs fsid=%v bsize=%d blocks=%d: the same filesystem",
			viaFd.Fsid, viaFd.Bsize, viaFd.Blocks, viaPath.Fsid, viaPath.Bsize, viaPath.Blocks)
	}

	var exported Statfs_t
	if err := syscall.Statfs(dir, &exported); err != nil {
		t.Fatalf("Statfs: %v", err)
	}
	if exported.Fsid != viaPath.Fsid || exported.Blocks != viaPath.Blocks {
		t.Errorf("Statfs fsid=%v blocks=%d, raw statfs fsid=%v blocks=%d: the same filesystem",
			exported.Fsid, exported.Blocks, viaPath.Fsid, viaPath.Blocks)
	}

	missing, err := syscall.BytePtrFromString(dir + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	var none Statfs_t
	if _, _, e := syscall.Syscall(syscall.SYS_STATFS, uintptr(unsafe.Pointer(missing)), uintptr(unsafe.Pointer(&none)), 0); e != syscall.ENOENT {
		t.Errorf("statfs of a missing path: errno %v, want ENOENT", e)
	}
}

func checkStatfs(t *testing.T, what string, st *Statfs_t) {
	t.Helper()
	if st.Bsize <= 0 || st.Blocks == 0 {
		t.Errorf("%s: bsize %d blocks %d, want both positive", what, st.Bsize, st.Blocks)
	}
	// Available is a subset of free, which is a subset of the whole. A
	// record copied from the wrong offsets breaks the ordering.
	if st.Bfree > st.Blocks || st.Bavail > st.Bfree {
		t.Errorf("%s: blocks %d bfree %d bavail %d, want bavail <= bfree <= blocks", what, st.Blocks, st.Bfree, st.Bavail)
	}
}

// TestLinuxStructSizes pins the Linux side of the conversions. The
// Linux kernel fills these on a Linux host, so a field change here is a
// buffer the kernel overruns there.
func TestLinuxStructSizes(t *testing.T) {
	if got := unsafe.Sizeof(syscall.Statfs_t{}); got != 120 {
		t.Errorf("sizeof(Statfs_t) = %d, want 120", got)
	}
	if got := unsafe.Sizeof(syscall.Utsname{}); got != 6*65 {
		t.Errorf("sizeof(Utsname) = %d, want %d", got, 6*65)
	}
	if got := unsafe.Sizeof(syscall.Rlimit{}); got != 16 {
		t.Errorf("sizeof(Rlimit) = %d, want 16", got)
	}
}

func TestDarwinUtsnameToLinux(t *testing.T) {
	var src cosmo.DarwinUtsname
	copy(src.Sysname[:], "Darwin\x00")
	copy(src.Nodename[:], "host.local\x00")
	copy(src.Release[:], "24.0.0\x00")
	copy(src.Machine[:], "arm64\x00")
	// A field longer than the Linux 65-byte one must be truncated with
	// its terminator kept, not run off the end.
	for i := range src.Version {
		src.Version[i] = 'v'
	}

	var dst Utsname
	darwinUtsnameToLinux(&dst, &src)

	str := func(b [65]byte) string {
		n := 0
		for n < len(b) && b[n] != 0 {
			n++
		}
		return string(b[:n])
	}
	if got := str(dst.Sysname); got != "Darwin" {
		t.Errorf("Sysname = %q, want %q", got, "Darwin")
	}
	if got := str(dst.Nodename); got != "host.local" {
		t.Errorf("Nodename = %q, want %q", got, "host.local")
	}
	if got := str(dst.Release); got != "24.0.0" {
		t.Errorf("Release = %q, want %q", got, "24.0.0")
	}
	if got := str(dst.Machine); got != "arm64" {
		t.Errorf("Machine = %q, want %q", got, "arm64")
	}
	if got := str(dst.Version); len(got) != 64 {
		t.Errorf("Version truncated to %d bytes, want 64 plus a NUL", len(got))
	}
	if dst.Version[64] != 0 {
		t.Error("Version is not NUL-terminated after truncation")
	}
	// Apple has no domainname, so the field stays empty instead of
	// carrying an invented value.
	if got := str(dst.Domainname); got != "" {
		t.Errorf("Domainname = %q, want empty", got)
	}
}
