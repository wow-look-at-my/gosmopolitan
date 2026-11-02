package apetest

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Activity Monitor must name an APE after the program, not after its
// loader. The test reads the process table on the screen through the
// accessibility interface, which needs a logged-in session that allows it.
// APETEST_GUI=1 says the host has one. The macOS test leg sets it.
func TestActivityMonitorShowsTheProgram(t *testing.T) {
	if os.Getenv("APETEST_GUI") != "1" {
		t.Skip("APETEST_GUI=1 is not set, so this host has no screen to read")
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "testdata/activitymonitor.applescript", "runtimeprobe", "apeld").CombinedOutput()
	require.NoError(t, err, "osascript: %s", out)
	t.Logf("Activity Monitor: %s", strings.TrimSpace(string(out)))
	assert.Contains(t, string(out), name, "Activity Monitor must name the program")
	for _, shown := range strings.Split(string(out), "\n") {
		assert.NotContains(t, shown, "apeld", "Activity Monitor must not name the loader")
	}
}
