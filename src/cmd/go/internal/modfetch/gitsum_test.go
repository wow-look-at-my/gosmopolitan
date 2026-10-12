package modfetch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
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

// writeCacheFile writes body as mod's download cache file with suffix.
func writeCacheFile(t *testing.T, mod module.Version, suffix, body string) string {
	path, err := CachePath(context.Background(), mod, suffix)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o777))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o666))
	return path
}

func TestSumIsGoSumH1BesideGitZiphash(t *testing.T) {
	useModCache(t)
	mod := module.Version{Path: "github.com/google/uuid", Version: "v1.6.0"}
	const h1 = "h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0="
	writeCacheFile(t, mod, "ziphash", "git:"+testCommit)
	fetcher := sumFetcher(t, "github.com/google/uuid v1.6.0 "+h1+"\ngithub.com/google/uuid v1.6.0 git:"+testCommit+"\n")

	assert.Equal(t, h1, fetcher.Sum(context.Background(), mod))
}

func TestSumOfGitHubArchiveIsItsH1(t *testing.T) {
	useModCache(t)
	ctx := context.Background()
	mod := module.Version{Path: "github.com/google/uuid", Version: "v1.6.0"}
	files := memFiles(map[string]string{"go.mod": "module github.com/google/uuid\n", "uuid.go": "package uuid\n"})
	valid, err := checkModuleFiles(mod, files)
	require.NoError(t, err)
	want, err := moduleFilesSum(mod, valid)
	require.NoError(t, err)

	ziphash, err := CachePath(ctx, mod, "ziphash")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(ziphash), 0o777))
	fetcher := sumFetcher(t, "")
	require.NoError(t, fetcher.writeFiles(ctx, mod, files, testCommit, ziphash))
	data, err := os.ReadFile(ziphash)
	require.NoError(t, err)
	assert.Equal(t, "git:"+testCommit, string(data), "the archive is checked by its commit")
	assert.Equal(t, want, fetcher.Sum(ctx, mod))

	// A cache entry written before the h1 record existed hashes its directory.
	record, err := CachePath(ctx, mod, "h1")
	require.NoError(t, err)
	require.NoError(t, os.Remove(record))
	assert.Equal(t, want, fetcher.Sum(ctx, mod))

	// The hash is recorded, so the next build reads it instead.
	data, err = os.ReadFile(record)
	require.NoError(t, err)
	assert.Equal(t, want, string(data))
}

func TestSumOfGitZipIsItsH1(t *testing.T) {
	useModCache(t)
	ctx := context.Background()
	mod := module.Version{Path: "github.com/google/uuid", Version: "v1.6.0"}
	zipfile := writeCacheFile(t, mod, "zip", "")
	out, err := os.Create(zipfile)
	require.NoError(t, err)
	require.NoError(t, modzip.Create(out, mod, memFiles(map[string]string{"go.mod": "module github.com/google/uuid\n", "uuid.go": "package uuid\n"})))
	require.NoError(t, out.Close())
	writeCacheFile(t, mod, "ziphash", "git:"+testCommit)
	want, err := dirhash.HashZip(zipfile, dirhash.DefaultHash)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(want, "h1:"))

	assert.Equal(t, want, sumFetcher(t, "").Sum(ctx, mod))
}
