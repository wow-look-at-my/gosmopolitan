// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

const (
	_EVFILT_TIMER  = -0x7
	_EV_ONESHOT    = 0x10
	_NOTE_USECONDS = 0x2
	_NOTE_CRITICAL = 0x20
)

// sysmonKq is the kqueue sysmon sleeps on. sysmon creates it on its first
// sleep and is its only user.
var sysmonKq int32 = -1

// sysmonSleep sleeps for usec microseconds on a NOTE_CRITICAL kqueue timer.
// XNU coalesces an ordinary timer of a background-QoS process by tens of
// milliseconds, which stretches sysmon's ticks past the preemption time
// slice. A critical timer is exempt from that coalescing.
func sysmonSleep(usec uint32) {
	if sysmonKq < 0 {
		kq := kqueue()
		if kq < 0 {
			println("runtime: sysmon kqueue failed with", -kq)
			throw("runtime: sysmon kqueue failed")
		}
		closeonexec(kq)
		sysmonKq = kq
	}
	change := keventt{
		filter: _EVFILT_TIMER,
		flags:  _EV_ADD | _EV_ONESHOT,
		fflags: _NOTE_USECONDS | _NOTE_CRITICAL,
		data:   int64(usec),
	}
	var fired keventt
	if n := kevent(sysmonKq, &change, 1, &fired, 1, nil); n < 0 && n != -_EINTR {
		println("runtime: sysmon kevent failed with", -n)
		throw("runtime: sysmon kevent failed")
	}
}
