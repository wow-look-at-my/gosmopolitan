// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// printpath prints $PATH. The APE boot script runs its own tools under an
// augmented PATH, and this reports what the program is left holding.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println(os.Getenv("PATH"))
}
