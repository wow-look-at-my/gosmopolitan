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
// loader. The test reads Activity Monitor's own window through the
// accessibility interface while a probe runs. That needs a logged-in screen.
// APETEST_GUI=1 says the host has one, and the macOS test leg sets it.
func TestActivityMonitorShowsTheProgram(t *testing.T) {
	if os.Getenv("APETEST_GUI") != "1" {
		t.Skip("APETEST_GUI=1 is not set, so this host has no screen to read")
	}
	reader := filepath.Join(t.TempDir(), "activitymonitor")
	built, err := exec.Command("xcrun", "swiftc", "-O", "-o", reader, "testdata/activitymonitor.swift").CombinedOutput()
	require.NoError(t, err, "swiftc: %s", built)

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

	require.NoError(t, exec.Command("open", "-a", "Activity Monitor").Run())
	t.Cleanup(func() { exec.Command("killall", "Activity Monitor").Run() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, reader, "runtimeprobe", "apeld").CombinedOutput()
	shown := strings.Split(strings.TrimSpace(string(out)), "\n")
	t.Logf("Activity Monitor shows: %q", shown)
	require.NoError(t, err, "the reader: %s", out)
	assert.Contains(t, shown, name, "Activity Monitor must name the program")
	for _, text := range shown {
		assert.NotContains(t, text, "apeld", "Activity Monitor must not name the loader")
	}
}
