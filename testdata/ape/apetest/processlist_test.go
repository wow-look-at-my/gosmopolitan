package apetest

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ps, top and htop must show an APE as the program that was started, never as
// its loader. This test asks the host's own ps and top about a running probe.
// htop reads the same argv and process name as ps. btop and Activity Monitor
// read the executable path and the name that ps -o comm shows on macOS.
func TestProcessListShowsTheProgram(t *testing.T) {
	skipWhereNoLoaderBoots(t)
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
	first, err := lines.ReadString('\n')
	require.NoError(t, err, "the probe printed nothing")
	go io.Copy(io.Discard, lines)

	// The boot script and the loader exec in place, so the shell's pid is the program's.
	pid := strconv.Itoa(cmd.Process.Pid)
	require.Equal(t, "proccontrol-child "+pid, strings.TrimSpace(first))

	psTool := hostTool(t, "ps")
	ps := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(psTool, append(args, "-p", pid)...).Output()
		require.NoError(t, err, "ps %v", args)
		return strings.TrimSpace(string(out))
	}
	args := ps("-o", "args=")
	t.Logf("ps -o args: %s", args)
	assert.Equal(t, bin+" one arg two", args, "ps must show the program's argv")

	comm := ps("-o", "comm=")
	t.Logf("ps -o comm: %s", comm)
	want := name
	if runtime.GOOS == "linux" && len(want) > 15 {
		// The kernel keeps a name of up to TASK_COMM_LEN-1 bytes.
		want = want[:15]
	}
	assert.Equal(t, want, filepath.Base(comm), "ps must name the program")

	topTool := hostTool(t, "top")
	var top []byte
	if runtime.GOOS == "darwin" {
		top, err = exec.Command(topTool, "-l", "1", "-pid", pid, "-stats", "pid,command").Output()
	} else {
		// -w widens the batch output, so top does not cut the name to fit.
		top, err = exec.Command(topTool, "-b", "-n", "1", "-w", "512", "-p", pid).Output()
	}
	require.NoError(t, err, "top")
	rows := strings.Split(strings.TrimSpace(string(top)), "\n")
	row := strings.Fields(rows[len(rows)-1])
	t.Logf("top: %s", rows[len(rows)-1])
	require.NotEmpty(t, row)
	assert.Equal(t, pid, row[0], "top must list the program's pid last")
	assert.Equal(t, want, row[len(row)-1], "top must name the program")
}

// hostTool returns the path of the host's own tool. macOS installs ps and top
// setuid root, and a sandbox refuses to exec a setuid file. A copy of the
// tool loses the setuid bit, and it still reads this user's own processes.
// The copy runs only when the host refuses the tool itself.
func hostTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	require.NoError(t, err)
	if err := exec.Command(path, "-h").Run(); !errors.Is(err, syscall.EPERM) {
		return path
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	tool := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(tool, data, 0o755))
	t.Logf("this host refuses to exec %s, which is setuid, so the test runs a copy without the setuid bit", path)
	return tool
}
