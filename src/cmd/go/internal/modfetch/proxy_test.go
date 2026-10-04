package modfetch

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"cmd/go/internal/cfg"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

// useProxies makes proxyList parse goproxy, with no GONOPROXY, for the rest of t.
// It edits package state, so t runs alone.
func useProxies(t *testing.T, goproxy string) {
	t.Serial()
	oldProxy, oldNoProxy := cfg.GOPROXY, cfg.GONOPROXY
	reset := func() {
		proxyOnce.Once = sync.Once{}
		proxyOnce.list, proxyOnce.err = nil, nil
	}
	cfg.GOPROXY, cfg.GONOPROXY = goproxy, ""
	reset()
	t.Cleanup(func() {
		cfg.GOPROXY, cfg.GONOPROXY = oldProxy, oldNoProxy
		reset()
	})
}

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
	useProxies(t, cfg.DefaultGOPROXY)
	outage := errors.New("github.com: connection reset")
	fail := func(proxy string) error {
		if proxy == "github" {
			return outage
		}
		return notExistErrorf("404 Not Found")
	}
	assert.Equal(t, []string{"github"}, tryOrder(t, fail),
		"the archive route must come first and not run a second time as direct")
	assert.ErrorIs(t, TryProxies(fail), outage)
}

func TestTryProxiesNotGitHubReachesDirect(t *testing.T) {
	useProxies(t, cfg.DefaultGOPROXY)
	order := tryOrder(t, func(proxy string) error {
		if proxy == "github" {
			return errNotGitHub
		}
		return notExistErrorf("404 Not Found")
	})
	assert.Equal(t, []string{"github", "direct"}, order, "a path off github.com must go direct")
}

func TestDefaultNeverAsksProxyGolangOrg(t *testing.T) {
	useProxies(t, cfg.DefaultGOPROXY)
	list, err := proxyList()
	require.NoError(t, err)
	for _, spec := range list {
		assert.NotContains(t, spec.url, bannedProxyHost)
	}
	for _, base := range []string{"https://proxy.golang.org", "https://proxy.golang.org/", "http://proxy.golang.org/x"} {
		_, err := newProxyRepo(base, "golang.org/x/sync")
		assert.ErrorContains(t, err, bannedProxyHost, "%s must be refused", base)
	}
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
