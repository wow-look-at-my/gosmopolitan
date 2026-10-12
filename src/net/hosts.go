// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import (
	"errors"
	"internal/netconf"
	"internal/testlog"
	"io/fs"
	"sync"
	"time"
)

const cacheMaxAge = 5 * time.Second

func parseLiteralIP(addr string) string {
	return netconf.AddrKey(addr)
}

// hosts contains known host entries.
var hosts struct {
	sync.Mutex

	// Key for the list of literal IP addresses must be a host
	// name. It would be part of DNS labels, a FQDN or an absolute
	// FQDN.
	// For now the key is converted to lower case for convenience.
	byName map[string]netconf.ByName

	// Key for the list of host names must be a literal IP address
	// including IPv6 address with zone identifier.
	// We don't support old-classful IP address notation.
	byAddr map[string][]string

	expire time.Time
	path   string
	mtime  time.Time
	size   int64
}

func readHosts() {
	now := time.Now()
	hp := hostsFilePath

	if now.Before(hosts.expire) && hosts.path == hp && len(hosts.byName) > 0 {
		return
	}
	// A test's input is the answer each lookup logs, not the file.
	testlog.Parse("stat", hp)
	mtime, size, err := stat(hp)
	if err == nil && hosts.path == hp && hosts.mtime.Equal(mtime) && hosts.size == size {
		hosts.expire = now.Add(cacheMaxAge)
		return
	}

	testlog.Parse("open", hp)
	file, err := open(hp)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
			return
		}
	}

	hs := make(map[string]netconf.ByName)
	is := make(map[string][]string)
	if file != nil {
		defer file.close()
		hs, is = netconf.ReadHosts(file.file)
	}
	// Update the data cache.
	hosts.expire = now.Add(cacheMaxAge)
	hosts.path = hp
	hosts.byName = hs
	hosts.byAddr = is
	hosts.mtime = mtime
	hosts.size = size
}

// lookupStaticHost looks up the addresses and the canonical name for the given host from /etc/hosts.
func lookupStaticHost(host string) ([]string, string) {
	hosts.Lock()
	defer hosts.Unlock()
	testlog.Lookup("hostsname", hostsFilePath, host)
	readHosts()
	if len(hosts.byName) != 0 {
		if byName, ok := hosts.byName[netconf.NameKey(host)]; ok {
			ipsCp := make([]string, len(byName.Addrs))
			copy(ipsCp, byName.Addrs)
			return ipsCp, byName.Canonical
		}
	}
	return nil, ""
}

// lookupStaticAddr looks up the hosts for the given address from /etc/hosts.
func lookupStaticAddr(addr string) []string {
	hosts.Lock()
	defer hosts.Unlock()
	testlog.Lookup("hostsaddr", hostsFilePath, addr)
	readHosts()
	addr = parseLiteralIP(addr)
	if addr == "" {
		return nil
	}
	if len(hosts.byAddr) != 0 {
		if hosts, ok := hosts.byAddr[addr]; ok {
			hostsCp := make([]string, len(hosts))
			copy(hostsCp, hosts)
			return hostsCp
		}
	}
	return nil
}
