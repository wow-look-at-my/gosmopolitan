// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo || !cosmontdebug

package runtime

//go:nosplit
func ntBoot(msg string) {}

//go:nosplit
func ntBootCode(msg string, v uintptr) {}
