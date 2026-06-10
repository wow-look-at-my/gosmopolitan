package apetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func copyBinary(t *testing.T) string {
	t.Helper()
	src := os.Getenv("FIZZBUZZ_BIN")
	require.NotEmpty(t, src, "FIZZBUZZ_BIN must be set")

	tmp := filepath.Join(t.TempDir(), "fizzbuzz.com")
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmp, data, 0755))
	return tmp
}

func runFizzbuzz(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	bin := copyBinary(t)

	// APE binaries need to be invoked through a shell on Linux/Unix because
	// the kernel doesn't recognize the APE format directly. The shell will
	// parse the APE header as a script and execute the bootstrap code.
	// On Windows, the binary is recognized as a PE executable directly.
	// On macOS, the binary may need shell execution for the dd bootstrap.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// Windows recognizes APE as PE directly
		cmd = exec.Command(bin, args...)
	default:
		// Unix: invoke through shell for APE bootstrap
		shellArgs := append([]string{bin}, args...)
		cmd = exec.Command("/bin/sh", shellArgs...)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.Len() > 0 || err != nil {
		t.Logf("runFizzbuzz %v: err=%v stderr=%q", args, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// FizzBuzz tests (divisible by both 3 and 5)

func TestFizzbuzz_15(t *testing.T) {
	out, _, err := runFizzbuzz(t, "10", "5")
	require.NoError(t, err)
	assert.Equal(t, "fizzbuzz", out)
}

func TestFizzbuzz_30(t *testing.T) {
	out, _, err := runFizzbuzz(t, "15", "15")
	require.NoError(t, err)
	assert.Equal(t, "fizzbuzz", out)
}

func TestFizzbuzz_Zero(t *testing.T) {
	out, _, err := runFizzbuzz(t, "0", "0")
	require.NoError(t, err)
	assert.Equal(t, "fizzbuzz", out)
}

func TestFizzbuzz_45(t *testing.T) {
	out, _, err := runFizzbuzz(t, "45", "0")
	require.NoError(t, err)
	assert.Equal(t, "fizzbuzz", out)
}

func TestFizzbuzz_Negative15(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-15", "0")
	require.NoError(t, err)
	assert.Equal(t, "fizzbuzz", out)
}

func TestFizzbuzz_NegativeSum(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-10", "-5")
	require.NoError(t, err)
	assert.Equal(t, "fizzbuzz", out)
}

// Fizz tests (divisible by 3, not by 5)

func TestFizz_3(t *testing.T) {
	out, _, err := runFizzbuzz(t, "2", "1")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

func TestFizz_6(t *testing.T) {
	out, _, err := runFizzbuzz(t, "3", "3")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

func TestFizz_9(t *testing.T) {
	out, _, err := runFizzbuzz(t, "5", "4")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

func TestFizz_12(t *testing.T) {
	out, _, err := runFizzbuzz(t, "10", "2")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

func TestFizz_Negative3(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-3", "0")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

func TestFizz_Negative6(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-6", "0")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

// Buzz tests (divisible by 5, not by 3)

func TestBuzz_5(t *testing.T) {
	out, _, err := runFizzbuzz(t, "3", "2")
	require.NoError(t, err)
	assert.Equal(t, "buzz", out)
}

func TestBuzz_10(t *testing.T) {
	out, _, err := runFizzbuzz(t, "5", "5")
	require.NoError(t, err)
	assert.Equal(t, "buzz", out)
}

func TestBuzz_20(t *testing.T) {
	out, _, err := runFizzbuzz(t, "10", "10")
	require.NoError(t, err)
	assert.Equal(t, "buzz", out)
}

func TestBuzz_25(t *testing.T) {
	out, _, err := runFizzbuzz(t, "20", "5")
	require.NoError(t, err)
	assert.Equal(t, "buzz", out)
}

func TestBuzz_Negative5(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-5", "0")
	require.NoError(t, err)
	assert.Equal(t, "buzz", out)
}

func TestBuzz_Negative10(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-10", "0")
	require.NoError(t, err)
	assert.Equal(t, "buzz", out)
}

// Number tests (not divisible by 3 or 5)

func TestNumber_1(t *testing.T) {
	out, _, err := runFizzbuzz(t, "1", "0")
	require.NoError(t, err)
	assert.Equal(t, "1", out)
}

func TestNumber_2(t *testing.T) {
	out, _, err := runFizzbuzz(t, "1", "1")
	require.NoError(t, err)
	assert.Equal(t, "2", out)
}

func TestNumber_4(t *testing.T) {
	out, _, err := runFizzbuzz(t, "2", "2")
	require.NoError(t, err)
	assert.Equal(t, "4", out)
}

func TestNumber_7(t *testing.T) {
	out, _, err := runFizzbuzz(t, "3", "4")
	require.NoError(t, err)
	assert.Equal(t, "7", out)
}

func TestNumber_8(t *testing.T) {
	out, _, err := runFizzbuzz(t, "4", "4")
	require.NoError(t, err)
	assert.Equal(t, "8", out)
}

func TestNumber_11(t *testing.T) {
	out, _, err := runFizzbuzz(t, "5", "6")
	require.NoError(t, err)
	assert.Equal(t, "11", out)
}

func TestNumber_13(t *testing.T) {
	out, _, err := runFizzbuzz(t, "7", "6")
	require.NoError(t, err)
	assert.Equal(t, "13", out)
}

func TestNumber_Negative1(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-1", "0")
	require.NoError(t, err)
	assert.Equal(t, "-1", out)
}

func TestNumber_Negative2(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-2", "0")
	require.NoError(t, err)
	assert.Equal(t, "-2", out)
}

func TestNumber_Negative4(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-4", "0")
	require.NoError(t, err)
	assert.Equal(t, "-4", out)
}

// Large numbers

func TestLarge_1000(t *testing.T) {
	out, _, err := runFizzbuzz(t, "500", "500")
	require.NoError(t, err)
	assert.Equal(t, "buzz", out)
}

func TestLarge_999(t *testing.T) {
	out, _, err := runFizzbuzz(t, "501", "498")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

func TestLarge_1005(t *testing.T) {
	out, _, err := runFizzbuzz(t, "500", "505")
	require.NoError(t, err)
	assert.Equal(t, "fizzbuzz", out)
}

func TestLarge_19134(t *testing.T) {
	out, _, err := runFizzbuzz(t, "12345", "6789")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

// Mixed sign tests

func TestMixed_Fizz(t *testing.T) {
	out, _, err := runFizzbuzz(t, "10", "-7")
	require.NoError(t, err)
	assert.Equal(t, "fizz", out)
}

func TestMixed_Buzz(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-10", "15")
	require.NoError(t, err)
	assert.Equal(t, "buzz", out)
}

func TestMixed_Fizzbuzz(t *testing.T) {
	out, _, err := runFizzbuzz(t, "20", "-5")
	require.NoError(t, err)
	assert.Equal(t, "fizzbuzz", out)
}

func TestMixed_Number(t *testing.T) {
	out, _, err := runFizzbuzz(t, "-8", "10")
	require.NoError(t, err)
	assert.Equal(t, "2", out)
}

// Error handling

func TestError_NoArgs(t *testing.T) {
	_, stderr, err := runFizzbuzz(t)
	require.Error(t, err)
	assert.Contains(t, stderr, "Usage:")
}

func TestError_OneArg(t *testing.T) {
	_, stderr, err := runFizzbuzz(t, "5")
	require.Error(t, err)
	assert.Contains(t, stderr, "Usage:")
}

func TestError_TooManyArgs(t *testing.T) {
	_, stderr, err := runFizzbuzz(t, "1", "2", "3")
	require.Error(t, err)
	assert.Contains(t, stderr, "Usage:")
}

func TestError_InvalidFirst(t *testing.T) {
	_, stderr, err := runFizzbuzz(t, "abc", "5")
	require.Error(t, err)
	assert.Contains(t, stderr, "Invalid first argument")
}

func TestError_InvalidSecond(t *testing.T) {
	_, stderr, err := runFizzbuzz(t, "5", "xyz")
	require.Error(t, err)
	assert.Contains(t, stderr, "Invalid second argument")
}

func TestError_InvalidBoth(t *testing.T) {
	_, stderr, err := runFizzbuzz(t, "foo", "bar")
	require.Error(t, err)
	assert.Contains(t, stderr, "Invalid")
}

func TestError_FloatFirst(t *testing.T) {
	_, stderr, err := runFizzbuzz(t, "3.14", "5")
	require.Error(t, err)
	assert.Contains(t, stderr, "Invalid first argument")
}

func TestError_FloatSecond(t *testing.T) {
	_, stderr, err := runFizzbuzz(t, "5", "2.71")
	require.Error(t, err)
	assert.Contains(t, stderr, "Invalid second argument")
}
