// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// Package hostsfile reads a hosts file into the tables package net answers
// its static lookups from. The go command reads the same tables to key a
// cached test result on the answers a test was given, not on the whole file.
package hostsfile

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

// lineBuffer bounds a line. A longer line ends the read, as it always has in package net.
const lineBuffer = 64 * 1024

// Read parses the hosts file r holds. byName is keyed by NameKey of each
// name. byAddr is keyed by AddrKey of each address and lists its names
// rooted, in the case the file wrote them.
func Read(r io.Reader) (byName map[string]ByName, byAddr map[string][]string) {
	byName = make(map[string]ByName)
	byAddr = make(map[string][]string)
	lines := &lineReader{r: r, data: make([]byte, 0, lineBuffer)}
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

// Fields splits line at spaces, tabs, carriage returns and newlines.
func Fields(line string) []string {
	var fields []string
	last := 0
	for idx := 0; idx < len(line); idx++ {
		switch line[idx] {
		case ' ', '\r', '\t', '\n':
			if last < idx {
				fields = append(fields, line[last:idx])
			}
			last = idx + 1
		}
	}
	if last < len(line) {
		fields = append(fields, line[last:])
	}
	return fields
}

// lineReader hands out the lines of r, the last without a newline after it.
type lineReader struct {
	r     io.Reader
	data  []byte
	atEOF bool
}

func (lines *lineReader) fromData() (string, bool) {
	for idx, char := range lines.data {
		if char == '\n' {
			line := string(lines.data[:idx])
			rest := copy(lines.data, lines.data[idx+1:])
			lines.data = lines.data[:rest]
			return line, true
		}
	}
	if lines.atEOF && len(lines.data) > 0 {
		line := string(lines.data)
		lines.data = lines.data[:0]
		return line, true
	}
	return "", false
}

func (lines *lineReader) next() (string, bool) {
	if line, ok := lines.fromData(); ok {
		return line, true
	}
	if have := len(lines.data); have < cap(lines.data) {
		num, err := io.ReadFull(lines.r, lines.data[have:cap(lines.data)])
		lines.data = lines.data[:have+num]
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			lines.atEOF = true
		}
	}
	return lines.fromData()
}
