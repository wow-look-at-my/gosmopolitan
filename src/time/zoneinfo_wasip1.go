// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	"syscall"
)

// WASI preview 1 hosts do not guarantee any filesystem contents, so
// there is no /etc/localtime to consult, but a host may well preopen
// one of the standard zoneinfo directories.
var platformZoneSources = []string{
	"/usr/share/zoneinfo/",
	"/usr/share/lib/zoneinfo/",
	"/usr/lib/locale/TZ/",
}

func initLocal() {
	// Consult $TZ to find the time zone to use, as on Unix systems.
	// Unlike zoneinfo_unix.go we don't default to /etc/localtime when
	// $TZ is unset, since a WASI host does not guarantee that path
	// exists: no (or an empty) $TZ simply means UTC.
	// $TZ="foo" or $TZ=":foo" with foo an absolute path names a time
	// zone file directly; otherwise foo is looked up in $ZONEINFO, the
	// platform sources above (when the host preopened them), and the
	// embedded copy of the time zone database registered by importing
	// the time/tzdata package.
	if tz, ok := syscall.Getenv("TZ"); ok && tz != "" {
		if tz[0] == ':' {
			tz = tz[1:]
		}
		if tz != "" && tz[0] == '/' {
			if z, err := loadLocation(tz, []string{""}); err == nil {
				localLoc = *z
				if tz == "/etc/localtime" {
					localLoc.name = "Local"
				} else {
					localLoc.name = tz
				}
				return
			}
		} else if tz != "" && tz != "UTC" {
			if z, err := loadLocation(tz, platformZoneSources); err == nil {
				localLoc = *z
				return
			}
		}
	}

	// Fall back to UTC.
	localLoc.name = "UTC"
}
