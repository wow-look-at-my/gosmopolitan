// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package net

// interfacesServedHere is true everywhere but cosmo: a port that builds
// for one kernel implements the table for that kernel or does not build.
func interfacesServedHere() bool { return true }
