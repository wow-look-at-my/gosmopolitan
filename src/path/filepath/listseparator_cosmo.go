// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package filepath

import "os"

// ListSeparator is a variable here because one APE runs on hosts that disagree
// about it. os answers it from the host, and this follows.
var ListSeparator = os.PathListSeparator
