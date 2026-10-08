// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package netconf

import (
	"internal/bytealg"
	"internal/stringslite"
	"io"
	"net/netip"
	"time"
)

// Resolv is what a resolv.conf says. Servers and Search are empty when the
// file names none, and the caller supplies the host's defaults.
type Resolv struct {
	Servers       []string // literal IP addresses, at most three
	Search        []string // rooted suffixes to append to a local name
	Ndots         int
	Timeout       time.Duration
	Attempts      int
	Rotate        bool
	UnknownOpt    bool
	Lookup        []string // OpenBSD's lookup order
	SingleRequest bool
	UseTCP        bool
	TrustAD       bool
	NoReload      bool
}

// ReadResolv parses the resolv.conf r holds. See resolv.conf(5).
func ReadResolv(r io.Reader) Resolv {
	conf := Resolv{Ndots: 1, Timeout: 5 * time.Second, Attempts: 2}
	lines := newLineReader(r)
	for line, ok := lines.next(); ok; line, ok = lines.next() {
		if len(line) > 0 && (line[0] == ';' || line[0] == '#') {
			continue
		}
		fields := Fields(line)
		if len(fields) < 1 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			// A server named by anything but an address would need DNS to find.
			if len(fields) > 1 && len(conf.Servers) < 3 {
				if _, err := netip.ParseAddr(fields[1]); err == nil {
					conf.Servers = append(conf.Servers, fields[1])
				}
			}
		case "domain":
			if len(fields) > 1 {
				conf.Search = []string{ensureRooted(fields[1])}
			}
		case "search":
			conf.Search = make([]string, 0, len(fields)-1)
			for _, field := range fields[1:] {
				name := ensureRooted(field)
				if name == "." {
					continue
				}
				conf.Search = append(conf.Search, name)
			}
		case "options":
			for _, option := range fields[1:] {
				conf.option(option)
			}
		case "lookup":
			conf.Lookup = fields[1:]
		default:
			conf.UnknownOpt = true
		}
	}
	return conf
}

func (conf *Resolv) option(option string) {
	switch {
	case stringslite.HasPrefix(option, "ndots:"):
		conf.Ndots = min(max(leadingInt(option[len("ndots:"):]), 0), 15)
	case stringslite.HasPrefix(option, "timeout:"):
		conf.Timeout = time.Duration(max(leadingInt(option[len("timeout:"):]), 1)) * time.Second
	case stringslite.HasPrefix(option, "attempts:"):
		conf.Attempts = max(leadingInt(option[len("attempts:"):]), 1)
	case option == "rotate":
		conf.Rotate = true
	case option == "single-request" || option == "single-request-reopen":
		conf.SingleRequest = true
	case option == "use-vc" || option == "usevc" || option == "tcp":
		conf.UseTCP = true
	case option == "trust-ad":
		conf.TrustAD = true
	case option == "edns0":
		// EDNS is on by default.
	case option == "no-reload":
		conf.NoReload = true
	default:
		conf.UnknownOpt = true
	}
}

// leadingInt is the decimal number s starts with: 0 when it starts with no
// digit, and 0xFFFFFF once the digits pass it.
func leadingInt(s string) int {
	const big = 0xFFFFFF
	num := 0
	for idx := 0; idx < len(s) && '0' <= s[idx] && s[idx] <= '9'; idx++ {
		num = num*10 + int(s[idx]-'0')
		if num >= big {
			return big
		}
	}
	return num
}

// DefaultSearch is the search list of a resolv.conf that names none: the
// domain of hostname, when it has one.
func DefaultSearch(hostname string) []string {
	if idx := bytealg.IndexByteString(hostname, '.'); idx >= 0 && idx < len(hostname)-1 {
		return []string{ensureRooted(hostname[idx+1:])}
	}
	return nil
}

// NameList is the names a DNS lookup of name asks for, in order.
func NameList(name string, ndots int, search []string) []string {
	// Check name length (see isDomainName in package net).
	rooted := len(name) > 0 && name[len(name)-1] == '.'
	if len(name) > 254 || len(name) == 254 && !rooted {
		return nil
	}

	if rooted {
		if avoidDNS(name) {
			return nil
		}
		return []string{name}
	}

	hasNdots := bytealg.CountString(name, '.') >= ndots
	name += "."

	names := make([]string, 0, 1+len(search))
	if hasNdots && !avoidDNS(name) {
		names = append(names, name)
	}
	for _, suffix := range search {
		fqdn := name + suffix
		if !avoidDNS(fqdn) && len(fqdn) <= 254 {
			names = append(names, fqdn)
		}
	}
	if !hasNdots && !avoidDNS(name) {
		names = append(names, name)
	}
	return names
}

// avoidDNS reports whether name is one DNS is never asked for: .onion, per
// RFC 7686.
func avoidDNS(name string) bool {
	if name == "" {
		return true
	}
	name = stringslite.TrimSuffix(name, ".")
	const onion = ".onion"
	if len(name) < len(onion) {
		return false
	}
	for idx, char := range []byte(name[len(name)-len(onion):]) {
		if 'A' <= char && char <= 'Z' {
			char += 'a' - 'A'
		}
		if char != onion[idx] {
			return false
		}
	}
	return true
}

func ensureRooted(name string) string {
	if len(name) > 0 && name[len(name)-1] == '.' {
		return name
	}
	return name + "."
}
