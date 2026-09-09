// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo_test

import (
	"internal/runtime/syscall/cosmo"
	"testing"
	"unsafe"
)

// A POSIX record lock is the one fcntl family whose argument is a struct, and
// the two systems disagree about all three parts of it: the field order, the
// command numbers and the lock types. Refusing the family with ENOSYS hands a
// caller that locks a file to make its writes safe an I/O error it cannot act
// on. SQLite reports SQLITE_IOERR_LOCK for it, and a database never opens.
//
// Ground truth is upstream Go's own darwin/arm64 definitions, which are
// generated from Apple's headers: syscall.Flock_t in ztypes_darwin_arm64.go
// and the F_* numbers in zerrors_darwin_arm64.go. The Linux side is
// zerrors_cosmo_arm64.go. This arithmetic is host-independent, so the Linux CI
// leg runs it; a macOS run proves the fcntl wiring behind it.

// The struct is repacked field by field, never reinterpreted, so a layout
// drift has to fail here rather than corrupt a lock request in place.
func TestFlockLayoutsDiffer(t *testing.T) {
	var lk cosmo.LinuxFlockForTest
	if got, want := unsafe.Sizeof(lk), uintptr(32); got != want {
		t.Errorf("Linux struct flock is %d bytes, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(lk.Type), uintptr(0); got != want {
		t.Errorf("Linux l_type at %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(lk.Start), uintptr(8); got != want {
		t.Errorf("Linux l_start at %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(lk.Pid), uintptr(24); got != want {
		t.Errorf("Linux l_pid at %d, want %d", got, want)
	}
	var af cosmo.AppleFlockForTest
	if got, want := unsafe.Sizeof(af), uintptr(24); got != want {
		t.Errorf("Apple struct flock is %d bytes, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(af.Start), uintptr(0); got != want {
		t.Errorf("Apple l_start at %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(af.Pid), uintptr(16); got != want {
		t.Errorf("Apple l_pid at %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(af.Type), uintptr(20); got != want {
		t.Errorf("Apple l_type at %d, want %d", got, want)
	}
}

// Both numberings are distinct, so reusing either one unchanged asks Apple for
// a different command or a different lock than the caller wanted.
func TestFlockNumberingsDiffer(t *testing.T) {
	for _, c := range []struct {
		name         string
		linux, apple int
	}{
		{"F_GETLK", cosmo.LinuxF_GETLKForTest, cosmo.AppleF_GETLKForTest},
		{"F_SETLK", cosmo.LinuxF_SETLKForTest, cosmo.AppleF_SETLKForTest},
		{"F_SETLKW", cosmo.LinuxF_SETLKWForTest, cosmo.AppleF_SETLKWForTest},
		{"F_RDLCK", cosmo.LinuxF_RDLCKForTest, cosmo.AppleF_RDLCKForTest},
		{"F_WRLCK", cosmo.LinuxF_WRLCKForTest, cosmo.AppleF_WRLCKForTest},
	} {
		if c.linux == c.apple {
			t.Errorf("%s is %d on both systems, so the test fixture is wrong", c.name, c.linux)
		}
	}
	// Apple numbers a write lock 3 where Linux numbers an unlock 2, so an
	// untranslated F_WRLCK request would land somewhere real and wrong.
	if cosmo.AppleF_WRLCKForTest == cosmo.LinuxF_UNLCKForTest {
		t.Error("the fixture makes an untranslated write lock look like an unlock")
	}
}

func TestFlockToApple(t *testing.T) {
	for _, c := range []struct {
		name      string
		linuxType int16
		appleType int16
	}{
		{"read", cosmo.LinuxF_RDLCKForTest, cosmo.AppleF_RDLCKForTest},
		{"write", cosmo.LinuxF_WRLCKForTest, cosmo.AppleF_WRLCKForTest},
		{"unlock", cosmo.LinuxF_UNLCKForTest, cosmo.AppleF_UNLCKForTest},
	} {
		t.Run(c.name, func(t *testing.T) {
			lk := cosmo.LinuxFlockForTest{
				Type:   c.linuxType,
				Whence: 1,
				Start:  4096,
				Len:    512,
				Pid:    1234,
			}
			af, ok := cosmo.FlockToAppleForTest(&lk)
			if !ok {
				t.Fatalf("refused a %s lock", c.name)
			}
			if af.Type != c.appleType {
				t.Errorf("l_type = %d, want %d", af.Type, c.appleType)
			}
			if af.Whence != 1 || af.Start != 4096 || af.Len != 512 || af.Pid != 1234 {
				t.Errorf("fields did not survive: %+v", af)
			}
		})
	}
}

// F_GETLK answers in the caller's struct, so the reverse mapping carries the
// holder of a conflicting lock back. Getting it wrong reports the wrong holder.
func TestFlockFromApple(t *testing.T) {
	af := cosmo.AppleFlockForTest{
		Start:  8192,
		Len:    1024,
		Pid:    4321,
		Type:   cosmo.AppleF_WRLCKForTest,
		Whence: 0,
	}
	var lk cosmo.LinuxFlockForTest
	if !cosmo.FlockFromAppleForTest(&lk, &af) {
		t.Fatal("refused Apple's answer")
	}
	if lk.Type != cosmo.LinuxF_WRLCKForTest {
		t.Errorf("l_type = %d, want the Linux write lock %d", lk.Type, cosmo.LinuxF_WRLCKForTest)
	}
	if lk.Start != 8192 || lk.Len != 1024 || lk.Pid != 4321 || lk.Whence != 0 {
		t.Errorf("fields did not survive: %+v", lk)
	}
}

func TestFlockRoundTrip(t *testing.T) {
	for _, lt := range []int16{cosmo.LinuxF_RDLCKForTest, cosmo.LinuxF_WRLCKForTest, cosmo.LinuxF_UNLCKForTest} {
		want := cosmo.LinuxFlockForTest{Type: lt, Whence: 2, Start: -1, Len: 0, Pid: 7}
		af, ok := cosmo.FlockToAppleForTest(&want)
		if !ok {
			t.Fatalf("refused lock type %d", lt)
		}
		var got cosmo.LinuxFlockForTest
		if !cosmo.FlockFromAppleForTest(&got, &af) {
			t.Fatalf("refused the answer for lock type %d", lt)
		}
		if got != want {
			t.Errorf("round trip of lock type %d gave %+v, want %+v", lt, got, want)
		}
	}
}

// An unknown lock type must be refused. Mapping it to zero would silently turn
// it into a Linux read lock.
func TestFlockRefusesAnUnknownType(t *testing.T) {
	lk := cosmo.LinuxFlockForTest{Type: 99}
	if _, ok := cosmo.FlockToAppleForTest(&lk); ok {
		t.Error("accepted an unknown Linux lock type")
	}
	af := cosmo.AppleFlockForTest{Type: 99}
	var out cosmo.LinuxFlockForTest
	if cosmo.FlockFromAppleForTest(&out, &af) {
		t.Error("accepted an unknown Apple lock type")
	}
}
