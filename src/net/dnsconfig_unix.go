// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows

// Read system DNS config from /etc/resolv.conf

package net

import (
	"internal/netconf"
	"net/netip"
	"time"
)

// See resolv.conf(5) on a Linux machine.
func dnsReadConfig(filename string) *dnsConfig {
	conf := &dnsConfig{
		ndots:    1,
		timeout:  5 * time.Second,
		attempts: 2,
	}
	file, err := open(filename)
	if err != nil {
		conf.servers = unreadableConfServers()
		conf.search = dnsDefaultSearch()
		conf.err = err
		return conf
	}
	defer file.close()
	if fi, err := file.file.Stat(); err == nil {
		conf.mtime = fi.ModTime()
	} else {
		conf.servers = unreadableConfServers()
		conf.search = dnsDefaultSearch()
		conf.err = err
		return conf
	}
	parsed := netconf.ReadResolv(file.file)
	for _, server := range parsed.Servers {
		conf.servers = append(conf.servers, JoinHostPort(server, "53"))
	}
	conf.search = parsed.Search
	conf.ndots = parsed.Ndots
	conf.timeout = parsed.Timeout
	conf.attempts = parsed.Attempts
	conf.rotate = parsed.Rotate
	conf.unknownOpt = parsed.UnknownOpt
	conf.lookup = parsed.Lookup
	conf.singleRequest = parsed.SingleRequest
	conf.useTCP = parsed.UseTCP
	conf.trustAD = parsed.TrustAD
	conf.noReload = parsed.NoReload
	if len(conf.servers) == 0 {
		conf.servers = unreadableConfServers()
	}
	if len(conf.search) == 0 {
		conf.search = dnsDefaultSearch()
	}
	return conf
}

// unreadableConfServers answers when resolv.conf named no usable
// nameserver, because it was missing, unreadable, or empty.
//
// defaultNS is localhost, which is a guess that the machine runs its own
// resolver. A host that publishes its list somewhere this package cannot
// open (Windows, under a cosmo build) can be asked instead, and its
// answer is a measurement. Falling back to localhost there sends every
// query to a port nothing is listening on.
func unreadableConfServers() []string {
	if servers := nameserversFromHost(hostNameservers()); len(servers) > 0 {
		return servers
	}
	return defaultNS
}

// nameserversFromHost puts the host's answer into the form the resolver
// dials, applying the checks the resolv.conf parser applies to the same
// values: an address only, and no more than the small standard limit.
func nameserversFromHost(addrs []string) []string {
	var servers []string
	for _, addr := range addrs {
		if len(servers) == 3 { // the limit dnsReadConfig imposes above
			break
		}
		if _, err := netip.ParseAddr(addr); err != nil {
			continue
		}
		servers = append(servers, JoinHostPort(addr, "53"))
	}
	return servers
}

func dnsDefaultSearch() []string {
	hn, err := getHostname()
	if err != nil {
		// best effort
		return nil
	}
	return netconf.DefaultSearch(hn)
}

func ensureRooted(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s
	}
	return s + "."
}
