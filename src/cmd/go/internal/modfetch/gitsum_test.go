package modfetch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
)

const testCommit = "0f11ee6918f41a04c201eceeadf612a377bc7fbc"

func TestIsGitSum(t *testing.T) {
	assert.True(t, IsGitSum("git:"+testCommit))
	assert.False(t, IsGitSum("git:"+testCommit[:39]))
	assert.False(t, IsGitSum("git:"+testCommit[:39]+"z"))
	assert.False(t, IsGitSum("h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0="))
	assert.True(t, isValidSum([]byte("git:"+testCommit)))
}

// sumFetcher answers a Fetcher whose go.sum holds lines.
func sumFetcher(t *testing.T, lines string) *Fetcher {
	file := filepath.Join(t.TempDir(), "go.sum")
	require.NoError(t, os.WriteFile(file, []byte(lines), 0o666))
	fetcher := NewFetcher()
	fetcher.SetGoSumFile(file)
	return fetcher
}

func TestCommitSum(t *testing.T) {
	mod := module.Version{Path: "github.com/google/uuid", Version: "v1.6.0"}
	hashed := false
	hash := func() (string, error) {
		hashed = true
		return "h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=", nil
	}

	// No h1 line: the commit is the sum and nothing is hashed.
	sum, err := sumFetcher(t, "").commitSum(mod, testCommit, hash)
	require.NoError(t, err)
	assert.Equal(t, "git:"+testCommit, sum)
	assert.False(t, hashed, "a git sum must not hash the files")

	// A go.sum that another go command wrote is checked by its h1 sum.
	sum, err = sumFetcher(t, "github.com/google/uuid v1.6.0 h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=\n").commitSum(mod, testCommit, hash)
	require.NoError(t, err)
	assert.Equal(t, "h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=", sum)
	assert.True(t, hashed)

	// No commit: the h1 sum, as before.
	hashed = false
	sum, err = sumFetcher(t, "").commitSum(mod, "", hash)
	require.NoError(t, err)
	assert.True(t, hashed)
	assert.Equal(t, "h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=", sum)
}

func TestGitSumBesideH1Sum(t *testing.T) {
	mod := module.Version{Path: "github.com/google/uuid", Version: "v1.6.0"}
	fetcher := sumFetcher(t, "github.com/google/uuid v1.6.0 h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=\n")

	// A git sum beside an h1 line is no mismatch. It exits the process if it is.
	require.NoError(t, checkModSum(fetcher, mod, "git:"+testCommit))
	assert.True(t, HaveSum(fetcher, mod))

	// The h1 line wins, because every go command can check it.
	sum, ok := fetcher.RecordedSum(mod)
	assert.True(t, ok)
	assert.Equal(t, "h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=", sum)
}
