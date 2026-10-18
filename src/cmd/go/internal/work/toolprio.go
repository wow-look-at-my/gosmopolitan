// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"os/exec"
)

// toolNiceIncrement is how far below the go command's own priority
// "go test" runs the tools it builds with.
const toolNiceIncrement = 10

// runTool runs a build tool, the way cmd.Run does.
func runTool(cmd *exec.Cmd) error {
	return cmd.Run()
}
