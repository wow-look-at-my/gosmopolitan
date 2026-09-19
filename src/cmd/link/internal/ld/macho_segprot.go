// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"debug/macho"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// segprotRequest is one -segprot the external linker was asked for.
type segprotRequest struct {
	segment  string
	maxprot  uint32
	initprot uint32
}

// segprotRequests reads the -segprot flags among the external linker's
// flags, written either as -Wl,-segprot,SEG,MAX,INIT or as
// -segprot SEG MAX INIT.
//
// The Apple linkers take the flag and, for an arm64 __TEXT segment, leave
// the protections at r-x/r-x anyway, so the linker applies what was asked
// for to the output itself.
func segprotRequests(flags []string) ([]segprotRequest, error) {
	var requests []segprotRequest
	add := func(fields []string) error {
		maxprot, err := parseSegprot(fields[1])
		if err != nil {
			return err
		}
		initprot, err := parseSegprot(fields[2])
		if err != nil {
			return err
		}
		requests = append(requests, segprotRequest{segment: fields[0], maxprot: maxprot, initprot: initprot})
		return nil
	}
	for idx := 0; idx < len(flags); idx++ {
		if flags[idx] == "-segprot" {
			if idx+3 >= len(flags) {
				return nil, fmt.Errorf("-segprot needs a segment and two protections")
			}
			if err := add(flags[idx+1 : idx+4]); err != nil {
				return nil, err
			}
			idx += 3
			continue
		}
		parts, found := strings.CutPrefix(flags[idx], "-Wl,")
		if !found {
			continue
		}
		fields := strings.Split(parts, ",")
		for pos := 0; pos < len(fields); pos++ {
			if fields[pos] != "-segprot" {
				continue
			}
			if pos+3 >= len(fields) {
				return nil, fmt.Errorf("%s: -segprot needs a segment and two protections", flags[idx])
			}
			if err := add(fields[pos+1 : pos+4]); err != nil {
				return nil, err
			}
			pos += 3
		}
	}
	return requests, nil
}

// parseSegprot reads a protection the way the Apple linker does: letters
// from r, w, x and -, or a hexadecimal number.
func parseSegprot(value string) (uint32, error) {
	if hex, found := strings.CutPrefix(value, "0x"); found {
		prot, err := strconv.ParseUint(hex, 16, 32)
		return uint32(prot), err
	}
	var prot uint32
	for _, char := range value {
		switch char {
		case 'r':
			prot |= 1
		case 'w':
			prot |= 2
		case 'x':
			prot |= 4
		case '-':
		default:
			return 0, fmt.Errorf("bad -segprot protection %q", value)
		}
	}
	return prot, nil
}

// machoApplySegprot sets the protections of the named segments of the
// Mach-O file at path.
func machoApplySegprot(path string, requests []segprotRequest) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	exem, err := macho.NewFile(file)
	if err != nil {
		return err
	}
	if exem.Magic != macho.Magic64 {
		return fmt.Errorf("%s is not a 64-bit Mach-O file", path)
	}
	// A 64-bit Mach-O header is 32 bytes. In a segment_command_64, maxprot
	// and initprot follow the command, its size, the 16-byte name and four
	// 8-byte fields.
	const headerSize, maxprotOffset = 32, 56
	applied := make([]bool, len(requests))
	offset := int64(headerSize)
	for _, load := range exem.Loads {
		if seg, isSegment := load.(*macho.Segment); isSegment && seg.Cmd == macho.LoadCmdSegment64 {
			for idx, request := range requests {
				if seg.Name != request.segment {
					continue
				}
				var prots [8]byte
				exem.ByteOrder.PutUint32(prots[0:], request.maxprot)
				exem.ByteOrder.PutUint32(prots[4:], request.initprot)
				if _, err := file.WriteAt(prots[:], offset+maxprotOffset); err != nil {
					return err
				}
				applied[idx] = true
			}
		}
		offset += int64(len(load.Raw()))
	}
	for idx, request := range requests {
		if !applied[idx] {
			return fmt.Errorf("%s has no %s segment", path, request.segment)
		}
	}
	return file.Close()
}
