// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

const (
	_EVFILT_TIMER_xnu  = -0x7
	_EV_ONESHOT_xnu    = 0x10
	_NOTE_USECONDS_xnu = 0x2
	_NOTE_CRITICAL_xnu = 0x20
)

// sysmonKq is the kqueue sysmon sleeps on, on an XNU host. sysmon creates it on its first sleep and is its only user.
var sysmonKq int32 = -1

// sysmonSleep sleeps for usec microseconds. On an XNU host it waits on a
// NOTE_CRITICAL kqueue timer. XNU coalesces an ordinary timer of a
// background-QoS process by tens of milliseconds. This stretches sysmon's
// ticks past the preemption time slice. A critical timer is exempt.
func sysmonSleep(usec uint32) {
	if !isdarwin() {
		usleep(usec)
		return
	}
	if sysmonKq < 0 {
		kq, e := cosmoDarwinKqueue()
		if kq < 0 {
			println("runtime: sysmon kqueue failed with", e)
			throw("runtime: sysmon kqueue failed")
		}
		closeonexec(kq)
		sysmonKq = kq
	}
	change := keventt{
		filter: _EVFILT_TIMER_xnu,
		flags:  _EV_ADD_xnu | _EV_ONESHOT_xnu,
		fflags: _NOTE_USECONDS_xnu | _NOTE_CRITICAL_xnu,
		data:   int64(usec),
	}
	var fired keventt
	if n, e := cosmoDarwinKevent(sysmonKq, &change, 1, &fired, 1, nil); n < 0 && e != _EINTR {
		println("runtime: sysmon kevent failed with", e)
		throw("runtime: sysmon kevent failed")
	}
}
