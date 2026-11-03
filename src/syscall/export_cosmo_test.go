// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package syscall

import (
	"unsafe"
)

func Tcgetpgrp(fd int) (pgid int32, err error) {
	_, _, errno := Syscall6(SYS_IOCTL, uintptr(fd), uintptr(TIOCGPGRP), uintptr(unsafe.Pointer(&pgid)), 0, 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return pgid, nil
}

func Tcsetpgrp(fd int, pgid int32) (err error) {
	_, _, errno := Syscall6(SYS_IOCTL, uintptr(fd), uintptr(TIOCSPGRP), uintptr(unsafe.Pointer(&pgid)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// Exports for bigbuf_cosmo_test.go. The test has to live in package
// syscall_test - an in-package test cannot import testing here - so the
// macOS struct conversions need a window.

var (
	OpenAuxvForTest              = openAuxv
	DarwinStatfsToLinuxForTest   = darwinStatfsToLinux
	DarwinMntFlagsToLinuxForTest = darwinMntFlagsToLinux
	DarwinUtsnameToLinuxForTest  = darwinUtsnameToLinux
)

// Exports for inotify_cosmo_test.go.

type InotifyQueueForTest = inotifyQueue

const InotifyQueueMaxForTest = inotifyQueueMax

var KernelLeaseWaitsForTest = kernelLeaseWaits

func (queue *inotifyQueue) PushForTest(wd int32, mask, cookie uint32, name string) {
	queue.push(wd, mask, cookie, name)
}

func (queue *inotifyQueue) RecordsForTest() [][]byte {
	return queue.records
}

// InotifyRechecksForTest drives the recheck timer of an instance with no source.
type InotifyRechecksForTest struct{ ino inotifyInstance }

func (test *InotifyRechecksForTest) Recheck(path string, millis uint32, again func()) {
	test.ino.busy.L = &test.ino.mu
	test.ino.recheck(path, millis, again)
}

func (test *InotifyRechecksForTest) Age(millis uint32) { test.ino.ageRechecks(millis) }

func (test *InotifyRechecksForTest) Pending() int { return len(test.ino.rechecks) }

// InotifyLiveForTest counts the instances that have not let go of everything.
func InotifyLiveForTest() int {
	return int(inotifyLive.Load())
}

// InotifyDoneForTest returns what closes when an instance has let go of
// everything, or nil for any other descriptor.
func InotifyDoneForTest(fd int) <-chan struct{} {
	inotifies.Lock()
	defer inotifies.Unlock()
	if ino := inotifies.m[fd]; ino != nil {
		return ino.done
	}
	return nil
}

const (
	AppleMntRdonlyForTest  = appleMNT_RDONLY
	AppleMntSyncForTest    = appleMNT_SYNCHRONOUS
	AppleMntNoexecForTest  = appleMNT_NOEXEC
	AppleMntNosuidForTest  = appleMNT_NOSUID
	AppleMntNodevForTest   = appleMNT_NODEV
	AppleMntNoatimeForTest = appleMNT_NOATIME

	LinuxStRdonlyForTest  = linuxST_RDONLY
	LinuxStNosuidForTest  = linuxST_NOSUID
	LinuxStNodevForTest   = linuxST_NODEV
	LinuxStNoexecForTest  = linuxST_NOEXEC
	LinuxStSyncForTest    = linuxST_SYNCHRONOUS
	LinuxStNoatimeForTest = linuxST_NOATIME
)
