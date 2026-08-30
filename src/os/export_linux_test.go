// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The tag mirrors zero_copy_linux.go and pidfd_linux.go, which cosmo does not
// build: the hooks exported here do not exist there.
//go:build linux && !cosmo

package os

var (
	PollCopyFileRangeP  = &pollCopyFileRange
	PollSpliceFile      = &pollSplice
	GetPollFDAndNetwork = getPollFDAndNetwork
	CheckPidfdOnce      = checkPidfdOnce
)

const StatusDone = statusDone

func (p *Process) Status() processStatus {
	return processStatus(p.state.Load())
}
