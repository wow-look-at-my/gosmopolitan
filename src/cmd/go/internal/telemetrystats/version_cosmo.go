// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap && cosmo

package telemetrystats

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"syscall"

	"cmd/internal/telemetry/counter"
)

func incrementVersionCounters() {
	convert := func(nullterm []byte) string {
		end := bytes.IndexByte(nullterm, 0)
		if end < 0 {
			end = len(nullterm)
		}
		return string(nullterm[:end])
	}

	var v syscall.Utsname
	err := syscall.Uname(&v)
	if err != nil {
		counter.Inc(fmt.Sprintf("go/platform/host/%s/version:unknown-uname-error", runtime.GOOS))
		return
	}
	major, minor, ok := majorMinorCosmo(convert(v.Release[:]))
	if !ok {
		counter.Inc(fmt.Sprintf("go/platform/host/%s/version:unknown-bad-format", runtime.GOOS))
		return
	}
	counter.Inc(fmt.Sprintf("go/platform/host/%s/major-version:%s", runtime.GOOS, major))
	counter.Inc(fmt.Sprintf("go/platform/host/%s/version:%s-%s", runtime.GOOS, major, minor))
}

func majorMinorCosmo(v string) (string, string, bool) {
	firstDot := strings.Index(v, ".")
	if firstDot < 0 {
		return "", "", false
	}
	major := v[:firstDot]
	v = v[firstDot+len("."):]
	endMinor := strings.IndexAny(v, ".-_")
	if endMinor < 0 {
		endMinor = len(v)
	}
	minor := v[:endMinor]
	return major, minor, true
}
