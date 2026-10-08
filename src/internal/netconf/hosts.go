// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// Package netconf reads the hosts file and resolv.conf into what package net
// answers its lookups from. The go command reads them the same way to key a
// cached test result on the answers a test was given, not on whole files.
package netconf

import (
	"io"
	"net/netip"
)

// ByName is what a hosts file says about one name: every address listed for
// it, in file order, and the first name.
type ByName struct {
	Addrs     []string
	Canonical string
}

// ReadHosts parses the hosts file r holds. byName is keyed by NameKey of
// each name. byAddr is keyed by AddrKey of each address and lists its names
// rooted, in the case the file wrote them.
func ReadHosts(r io.Reader) (byName map[string]ByName, byAddr map[string][]string) {
	byName = make(map[string]ByName)
	byAddr = make(map[string][]string)
	lines := newLineReader(r)
	for line, ok := lines.next(); ok; line, ok = lines.next() {
		for idx := 0; idx < len(line); idx++ {
			if line[idx] == '#' {
				line = line[:idx]
				break
			}
		}
		fields := Fields(line)
		if len(fields) < 2 {
			continue
		}
		addr := AddrKey(fields[0])
		if addr == "" {
			continue
		}
		var canonical string
		for col, field := range fields[1:] {
			key := NameKey(field)
			if col == 0 {
				canonical = key
			}
			byAddr[addr] = append(byAddr[addr], Rooted(field))
			if entry, ok := byName[key]; ok {
				byName[key] = ByName{Addrs: append(entry.Addrs, addr), Canonical: entry.Canonical}
				continue
			}
			byName[key] = ByName{Addrs: []string{addr}, Canonical: canonical}
		}
	}
	return byName, byAddr
}

// NameKey is the key a lookup of host matches: ASCII lower case, and rooted.
func NameKey(host string) string {
	lower := []byte(host)
	for idx, char := range lower {
		if 'A' <= char && char <= 'Z' {
			lower[idx] = char + 'a' - 'A'
		}
	}
	return Rooted(string(lower))
}

// AddrKey is the key a lookup of addr matches: its canonical text, or "" for
// a string that is not a literal IP address.
func AddrKey(addr string) string {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return ""
	}
	return ip.String()
}

// Rooted adds the trailing dot to a name with a dot in it and none at its end.
func Rooted(name string) string {
	for idx := 0; idx < len(name); idx++ {
		if name[idx] == '.' {
			if name[len(name)-1] != '.' {
				return name + "."
			}
			return name
		}
	}
	return name
}
