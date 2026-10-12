// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import "os"

// ReleaseEnv names the buildhost release number the build stamps into every binary's build info.
const ReleaseEnv = "GOSMOPOLITAN_RELEASE"

// ReleaseRevisionEnv names the commit that release was built from.
const ReleaseRevisionEnv = "GOSMOPOLITAN_RELEASE_REVISION"

// appendReleaseSettings records the release number and its commit as the
// build settings "release" and "release.revision". Every binary carries them,
// with no declaration in the program: debug.ReadBuildInfo reads them back.
// They are separate from the vcs settings, which depend on -buildvcs and on
// the build running inside a checkout.
func appendReleaseSettings(appendSetting func(key, value string)) {
	if rel := os.Getenv(ReleaseEnv); rel != "" {
		appendSetting("release", rel)
	}
	if rev := os.Getenv(ReleaseRevisionEnv); rev != "" {
		appendSetting("release.revision", rev)
	}
}
