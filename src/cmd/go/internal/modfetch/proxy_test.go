package modfetch

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

// tryOrder answers the entries TryProxies hands to a function that fails each
// with fail(entry).
func tryOrder(t *testing.T, fail func(string) error) []string {
	var order []string
	err := TryProxies(func(proxy string) error {
		order = append(order, proxy)
		return fail(proxy)
	})
	require.Error(t, err)
	return order
}

func TestTryProxiesGitHubFirst(t *testing.T) {
	outage := errors.New("github.com: connection reset")
	order := tryOrder(t, func(proxy string) error {
		switch proxy {
		case "github":
			return outage
		case "noproxy":
			return errUseProxy
		}
		return notExistErrorf("404 Not Found")
	})
	require.NotEmpty(t, order)
	assert.Equal(t, "github", order[0], "the archive route must come before every proxy")
	assert.Contains(t, order, "https://proxy.golang.org", "a failed archive route must fall back to the proxy")
	assert.NotContains(t, order, "direct", "direct must not fetch a module the github entry already tried")

	err := TryProxies(func(proxy string) error {
		switch proxy {
		case "github":
			return outage
		case "noproxy":
			return errUseProxy
		}
		return notExistErrorf("404 Not Found")
	})
	assert.ErrorIs(t, err, outage, "the direct error from github.com must outrank a proxy 404")
}

func TestTryProxiesNotGitHubReachesDirect(t *testing.T) {
	order := tryOrder(t, func(proxy string) error {
		switch proxy {
		case "github":
			return errNotGitHub
		case "noproxy":
			return errUseProxy
		}
		return notExistErrorf("404 Not Found")
	})
	assert.Equal(t, "direct", order[len(order)-1], "a path off github.com must still reach direct")
}

func TestLookupGitHubRefusesOtherHosts(t *testing.T) {
	_, err := lookup(NewFetcher(), context.Background(), "github", "golang.org/x/text")
	assert.Equal(t, errNotGitHub, err)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

type memFile struct {
	path string
	fsys fstest.MapFS
}

func (file memFile) Path() string                 { return file.path }
func (file memFile) Lstat() (fs.FileInfo, error)   { return fs.Stat(file.fsys, file.path) }
func (file memFile) Open() (io.ReadCloser, error) { return file.fsys.Open(file.path) }

func memFiles(contents map[string]string) []modzip.File {
	fsys := fstest.MapFS{}
	var files []modzip.File
	for name, body := range contents {
		fsys[name] = &fstest.MapFile{Data: []byte(body), Mode: 0o644}
		files = append(files, memFile{name, fsys})
	}
	return files
}

func TestCheckRecordedH1(t *testing.T) {
	mod := module.Version{Path: "github.com/google/uuid", Version: "v1.6.0"}
	files := memFiles(map[string]string{"go.mod": "module github.com/google/uuid\n", "uuid.go": "package uuid\n"})
	valid, err := checkModuleFiles(mod, files)
	require.NoError(t, err)
	hash, err := moduleFilesSum(mod, valid)
	require.NoError(t, err)

	assert.NoError(t, sumFetcher(t, "").checkRecordedH1(mod, files), "no h1 line: nothing to match")
	assert.NoError(t, sumFetcher(t, mod.Path+" "+mod.Version+" "+hash+"\n").checkRecordedH1(mod, files))

	other := "h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0="
	err = sumFetcher(t, mod.Path+" "+mod.Version+" "+other+"\n").checkRecordedH1(mod, files)
	require.Error(t, err, "an archive that hashes differently from go.sum must be refused, not fatal")
	assert.True(t, strings.Contains(err.Error(), hash), "the error names the archive's sum: %v", err)
}
