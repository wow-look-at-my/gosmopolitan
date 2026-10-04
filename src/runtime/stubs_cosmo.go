// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import "unsafe"

func sbrk0() uintptr

//go:noescape
func access(name *byte, mode int32) int32
func connect(fd int32, addr unsafe.Pointer, len int32) int32
func socket(domain int32, typ int32, prot int32) int32
