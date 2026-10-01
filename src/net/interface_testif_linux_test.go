// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The setters below drive the ip command and depend on nothing the cosmo port
// lacks, so they live apart from the netlink tests in interface_linux_test.go.
// interface_unix_test.go calls them on every unix host, cosmo included.

package net

import (
	"fmt"
	"os/exec"
)

func (ti *testInterface) setBroadcast(suffix int) error {
	ti.name = fmt.Sprintf("gotest%d", suffix)
	xname, err := exec.LookPath("ip")
	if err != nil {
		return err
	}
	ti.setupCmds = append(ti.setupCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "link", "add", ti.name, "type", "dummy"},
	})
	ti.setupCmds = append(ti.setupCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "address", "add", ti.local, "peer", ti.remote, "dev", ti.name},
	})
	ti.teardownCmds = append(ti.teardownCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "address", "del", ti.local, "peer", ti.remote, "dev", ti.name},
	})
	ti.teardownCmds = append(ti.teardownCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "link", "delete", ti.name, "type", "dummy"},
	})
	return nil
}

func (ti *testInterface) setLinkLocal(suffix int) error {
	ti.name = fmt.Sprintf("gotest%d", suffix)
	xname, err := exec.LookPath("ip")
	if err != nil {
		return err
	}
	ti.setupCmds = append(ti.setupCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "link", "add", ti.name, "type", "dummy"},
	})
	ti.setupCmds = append(ti.setupCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "address", "add", ti.local, "dev", ti.name},
	})
	ti.teardownCmds = append(ti.teardownCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "address", "del", ti.local, "dev", ti.name},
	})
	ti.teardownCmds = append(ti.teardownCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "link", "delete", ti.name, "type", "dummy"},
	})
	return nil
}

func (ti *testInterface) setPointToPoint(suffix int) error {
	ti.name = fmt.Sprintf("gotest%d", suffix)
	xname, err := exec.LookPath("ip")
	if err != nil {
		return err
	}
	ti.setupCmds = append(ti.setupCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "tunnel", "add", ti.name, "mode", "gre", "local", ti.local, "remote", ti.remote},
	})
	ti.setupCmds = append(ti.setupCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "address", "add", ti.local, "peer", ti.remote, "dev", ti.name},
	})
	ti.teardownCmds = append(ti.teardownCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "address", "del", ti.local, "peer", ti.remote, "dev", ti.name},
	})
	ti.teardownCmds = append(ti.teardownCmds, &exec.Cmd{
		Path: xname,
		Args: []string{"ip", "tunnel", "del", ti.name, "mode", "gre", "local", ti.local, "remote", ti.remote},
	})
	return nil
}
