// errorcheckdir

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A "readonly var" is a variable only its own package may assign to. Every
// other package reads it as a value. See docs/READONLY-VARS.md.

package ignored
