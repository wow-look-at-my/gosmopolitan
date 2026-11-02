package apetest

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ansi matches the terminal control sequences in a captured screen.
var ansi = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|[()][0-9A-Za-z]|[=>78])`)

// htop and btop draw a terminal screen and have no batch mode. This test runs
// each in a pseudo-terminal through script, against a running probe, and
// reads the screen it drew. APETEST_TUI_TOOLS names the tools to run. The
// macOS test leg installs them and sets it.
func TestTUIProcessListsShowTheProgram(t *testing.T) {
	skipWhereNoLoaderBoots(t)
	tools := strings.Fields(os.Getenv("APETEST_TUI_TOOLS"))
	if len(tools) == 0 {
		t.Skip("APETEST_TUI_TOOLS names no tool to run")
	}
	bin := copyProbeBinary(t)
	name := filepath.Base(bin)

	cmd := exec.Command("/bin/sh", bin, "one arg", "two")
	cmd.Env = append(os.Environ(), "RUNTIMEPROBE_CHILD=proccontrol")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	lines := bufio.NewReader(stdout)
	_, err = lines.ReadString('\n')
	require.NoError(t, err, "the probe printed nothing")
	go io.Copy(io.Discard, lines)
	pid := strconv.Itoa(cmd.Process.Pid)

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			path, err := exec.LookPath(tool)
			require.NoError(t, err, "APETEST_TUI_TOOLS names %s, which is not installed", tool)
			run := map[string]string{
				"htop": path + " -p " + pid,
				"btop": "env LANG=C.UTF-8 " + path + " --utf-force",
			}[tool]
			require.NotEmpty(t, run, "no way to run %s", tool)
			screen := captureScreen(t, run)
			var rows []string
			for _, row := range strings.Split(screen, "\n") {
				fields := strings.Fields(row)
				if slices.Contains(fields, pid) {
					rows = append(rows, strings.Join(fields, " "))
				}
			}
			for _, row := range rows {
				t.Logf("%s: %s", tool, row)
			}
			require.NotEmpty(t, rows, "%s did not show the probe:\n%s", tool, screen)
			for _, row := range rows {
				assert.NotContains(t, row, "apeld", "%s must show the program, not its loader", tool)
			}
			want := name
			if runtime.GOOS == "linux" && tool == "btop" {
				// btop shows the kernel's name, which keeps up to TASK_COMM_LEN-1 bytes.
				want = want[:min(len(want), 15)]
			}
			assert.Contains(t, strings.Join(rows, "\n"), want, "%s must name the program", tool)
		})
	}
}

// captureScreen runs command in a 50x220 pseudo-terminal for a few seconds
// and returns what it drew, with the control sequences removed.
func captureScreen(t *testing.T, command string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "screen")
	shell := "stty rows 50 cols 220; exec " + command
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "script", "-q", out, "/bin/sh", "-c", shell)
	} else {
		cmd = exec.CommandContext(ctx, "script", "-qfc", shell, out)
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	// script stops at the end of its input, so the input stays open until the context ends.
	input, hold, err := os.Pipe()
	require.NoError(t, err)
	defer input.Close()
	defer hold.Close()
	cmd.Stdin = input
	cmd.Run() // The context ends the tool, which draws until it is stopped.
	raw, err := os.ReadFile(out)
	require.NoError(t, err, "script wrote no screen")
	// A cursor move starts a new row: btop draws by position, not by line.
	screen := cursorMove.ReplaceAllString(string(raw), "\n")
	return strings.ReplaceAll(ansi.ReplaceAllString(screen, " "), "\r", "\n")
}

// cursorMove matches a move of the cursor to a row and column.
var cursorMove = regexp.MustCompile(`\x1b\[[0-9]*(;[0-9]*)?[Hf]`)
