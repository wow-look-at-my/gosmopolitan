// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"cmd/go/internal/cfg"
	"fmt"
	"os/exec"
)

// toolNiceIncrement is how far below the go command's own priority
// "go test" runs the tools it builds with. A test binary runs against a
// deadline and a compile does not, so on a busy machine the CPU goes to
// the test binaries first. The machine stays fully used either way.
const toolNiceIncrement = 10

// runTool runs a build tool, the way cmd.Run does. Under "go test" the
// tool runs toolNiceIncrement below the go command's priority.
func runTool(cmd *exec.Cmd) error {
	if cfg.CmdName != "test" {
		return cmd.Run()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := lowerToolPriority(cmd.Process.Pid); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("lowering the priority of %s: %w", cmd.Path, err)
	}
	return cmd.Wait()
}
