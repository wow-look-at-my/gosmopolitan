// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// checkDNS resolves a name off this host.
//
// Every other socket check here is loopback, so nothing in this probe
// used to touch a resolver, and an APE on an NT host shipped for months
// unable to resolve anything at all: net compiles the resolv.conf
// reader on cosmo (its constraint is !windows), NT has no such file,
// and the fallback asks localhost, where nothing is listening. A
// consumer found it, not this suite.
//
// The name is resolved through net's own resolver, so this exercises
// whatever the platform actually feeds it: /etc/resolv.conf on a unix
// host, the runtime's GetNetworkParams answer on NT.
func checkDNS() {
	servers := hostDNSServers()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, dnsProbeName)
	if err != nil {
		// Name the servers the runtime offered: a lookup that failed
		// with none reported, on a host that has no resolv.conf, is a
		// different defect from one that failed with servers in hand.
		fail("dns", "LookupHost(%s): %v (host servers: %s)", dnsProbeName, err, describeServers(servers))
		return
	}
	if len(addrs) == 0 {
		fail("dns", "LookupHost(%s) returned no addresses and no error", dnsProbeName)
		return
	}
	ok("dns", fmt.Sprintf("%s -> %s (host servers: %s)", dnsProbeName, addrs[0], describeServers(servers)))
}

// dnsProbeName is resolved by the check above. A name every CI runner
// already reaches to do its job, so a red here is this port's problem
// rather than the endpoint's.
const dnsProbeName = "github.com"

func describeServers(servers []string) string {
	if len(servers) == 0 {
		return "none reported (this host publishes them in a file net reads)"
	}
	return strings.Join(servers, ",")
}
