// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codehost

import (
	"archive/zip"
	"bytes"
	"errors"
	"internal/testenv"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"cmd/go/internal/web/intercept"
)

func TestParseGitHubRemote(t *testing.T) {
	cases := []struct {
		remote string
		want   githubRepo
		isOK   bool
	}{
		{"https://github.com/wow-look-at-my/slopfix", githubRepo{"wow-look-at-my", "slopfix"}, true},
		{"https://github.com/wow-look-at-my/slopfix.git", githubRepo{"wow-look-at-my", "slopfix"}, true},
		{"http://github.com/owner/repo", githubRepo{}, false},
		{"https://github.com.evil.example/owner/repo", githubRepo{}, false},
		{"https://evil.example/github.com/owner/repo", githubRepo{}, false},
		{"https://github.com:443/owner/repo", githubRepo{}, false},
		{"https://user@github.com/owner/repo", githubRepo{}, false},
		{"https://github.com/owner/repo/extra", githubRepo{}, false},
		{"https://github.com/owner", githubRepo{}, false},
		{"https://github.com/owner/..", githubRepo{}, false},
		{"https://github.com/owner/repo?x=1", githubRepo{}, false},
		{"git@github.com:owner/repo.git", githubRepo{}, false},
	}
	for _, test := range cases {
		got, isOK := parseGitHubRemote(test.remote)
		if got != test.want || isOK != test.isOK {
			t.Errorf("parseGitHubRemote(%q) = %v, %v, want %v, %v", test.remote, got, isOK, test.want, test.isOK)
		}
	}
}

func TestGitHubArchiveSources(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	repo := githubRepo{"wow-look-at-my", "slopfix"}
	proxied := func(inner string) string { return "https://proxy.pazer.ai/?url=" + url.QueryEscape(inner) }
	cases := []struct {
		ref  string
		want []string
	}{
		{"refs/heads/master", []string{
			"https://github.com/wow-look-at-my/slopfix/archive/refs/heads/master.tar.gz",
			proxied("https://github.com/wow-look-at-my/slopfix/archive/refs/heads/master.tar.gz"),
			"https://github.com/wow-look-at-my/slopfix/archive/refs/heads/master.zip",
			proxied("https://github.com/wow-look-at-my/slopfix/archive/refs/heads/master.zip"),
		}},
		{"refs/tags/sub/v1.2.3", []string{
			"https://github.com/wow-look-at-my/slopfix/archive/refs/tags/sub/v1.2.3.tar.gz",
			proxied("https://github.com/wow-look-at-my/slopfix/archive/refs/tags/sub/v1.2.3.tar.gz"),
			"https://github.com/wow-look-at-my/slopfix/archive/refs/tags/sub/v1.2.3.zip",
			proxied("https://github.com/wow-look-at-my/slopfix/archive/refs/tags/sub/v1.2.3.zip"),
		}},
		{"HEAD", []string{
			"https://github.com/wow-look-at-my/slopfix/archive/" + hash + ".tar.gz",
			proxied("https://github.com/wow-look-at-my/slopfix/archive/" + hash + ".tar.gz"),
			"https://github.com/wow-look-at-my/slopfix/archive/" + hash + ".zip",
			proxied("https://github.com/wow-look-at-my/slopfix/archive/" + hash + ".zip"),
		}},
	}
	for _, test := range cases {
		var got []string
		for _, source := range repo.archiveSources(test.ref, hash) {
			got = append(got, source.url)
			host := "github.com"
			if strings.HasPrefix(source.url, "https://proxy.pazer.ai/") {
				host = "proxy.pazer.ai"
			}
			for _, other := range []string{"github.com", "codeload.github.com", "proxy.pazer.ai", "evil.example"} {
				wantAllowed := other == host || (host == "github.com" && other == "codeload.github.com")
				if source.allowHost(other) != wantAllowed {
					t.Errorf("%s: allowHost(%q) = %v, want %v", source.url, other, !wantAllowed, wantAllowed)
				}
			}
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("archiveSources(%q):\ngot  %q\nwant %q", test.ref, got, test.want)
		}
	}
}

// commitTime is the time every test commit carries.
var commitTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// gitIn runs git in dir with a fixed identity and date, and no user config.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	date := commitTime.Format(time.RFC3339)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_COMMITTER_DATE="+date,
	)
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if exitErr, isExit := err.(*exec.ExitError); isExit {
			stderr = exitErr.Stderr
		}
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out))
}

func makeSourceRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	testenv.MustHaveExecPath(t, "git")
	dir := t.TempDir()
	gitIn(t, dir, "init", "--object-format=sha1", "--initial-branch=master")
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
			t.Fatal(err)
		}
		if target, isLink := strings.CutPrefix(body, "symlink:"); isLink {
			if err := os.Symlink(target, full); err != nil {
				t.Fatal(err)
			}
			continue
		}
		mode := os.FileMode(0o666)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o777
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "files")
	gitIn(t, dir, "tag", "v1.0.0")
	return dir, gitIn(t, dir, "rev-parse", "HEAD")
}

// archiveOf is what GitHub serves for a commit: git archive run with the
// repository's own attributes and a "<repo>-<name>/" prefix.
func archiveOf(t *testing.T, dir, format, rev string) []byte {
	t.Helper()
	cmd := exec.Command("git", "archive", "--format="+format, "--prefix=repo-1.0.0/", rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git archive: %v", err)
	}
	return out
}

// zipEntries maps each file and symlink of a zip to its content. A symlink
// is marked, so a file cannot stand in for one.
func zipEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]string)
	for _, entry := range reader.File {
		if entry.Mode().IsDir() {
			continue
		}
		open, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(open)
		open.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			body = append([]byte("symlink:"), body...)
		}
		entries[entry.Name] = string(body)
	}
	return entries
}

var sourceFiles = map[string]string{
	"go.mod":         "module example.com/repo\n",
	"main.go":        "package main\n\nfunc main() {}\n",
	"sub/lib.go":     "package sub\n",
	"sub/go.mod":     "module example.com/repo/sub\n",
	"tool.sh":        "#!/bin/sh\necho hi\n",
	"crlf.txt":       "one\r\ntwo\r\n",
	".gitattributes": "*.txt -text\n",
}

func sourceFilesWithLink() map[string]string {
	files := make(map[string]string)
	for name, body := range sourceFiles {
		files[name] = body
	}
	if runtime.GOOS != "windows" {
		files["link.go"] = "symlink:main.go"
	}
	return files
}

// referenceZip is what the git path of ReadZip builds for rev: git archive
// with export-subst and export-ignore turned off.
func referenceZip(t *testing.T, dir, rev string) []byte {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := ensureGitAttributes(gitDir); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-c", "core.autocrlf=input", "-c", "core.eol=lf", "archive", "--format=zip", "--prefix="+archivePrefix, rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git archive: %v", err)
	}
	return out
}

func TestGitHubArchiveConversion(t *testing.T) {
	dir, hash := makeSourceRepo(t, sourceFilesWithLink())
	want := zipEntries(t, referenceZip(t, dir, hash))

	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			served := archiveOf(t, dir, format, hash)
			var archive []byte
			var when time.Time
			var err error
			if format == "zip" {
				archive, when, err = githubZipToArchive(served, hash)
			} else {
				archive, when, err = githubTarToArchive(bytes.NewReader(served), hash)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !when.Equal(commitTime) {
				t.Errorf("commit time = %v, want %v", when, commitTime)
			}
			if got := zipEntries(t, archive); !reflect.DeepEqual(got, want) {
				t.Errorf("converted archive differs from git archive:\ngot  %q\nwant %q", got, want)
			}
		})
	}
}

func TestGitHubArchiveRefusals(t *testing.T) {
	convert := func(t *testing.T, dir, format, hash string) error {
		served := archiveOf(t, dir, format, "v1.0.0")
		var err error
		if format == "zip" {
			_, _, err = githubZipToArchive(served, hash)
		} else {
			_, _, err = githubTarToArchive(bytes.NewReader(served), hash)
		}
		return err
	}

	t.Run("another commit", func(t *testing.T) {
		dir, _ := makeSourceRepo(t, sourceFiles)
		for _, format := range []string{"tar.gz", "zip"} {
			err := convert(t, dir, format, strings.Repeat("1", 40))
			if err == nil || !strings.Contains(err.Error(), "archive is of commit") {
				t.Errorf("%s: err = %v, want a commit mismatch", format, err)
			}
		}
	})

	t.Run("submodule", func(t *testing.T) {
		files := map[string]string{"go.mod": "module example.com/repo\n"}
		dir, _ := makeSourceRepo(t, files)
		gitIn(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("2", 40)+",vendor/dep")
		gitIn(t, dir, "commit", "-q", "-m", "gitlink")
		gitIn(t, dir, "tag", "-f", "v1.0.0")
		hash := gitIn(t, dir, "rev-parse", "HEAD")
		for _, format := range []string{"tar.gz", "zip"} {
			err := convert(t, dir, format, hash)
			if !errors.Is(err, errNoGitHubArchive) || !strings.Contains(err.Error(), "vendor/dep") {
				t.Errorf("%s: err = %v, want the submodule named", format, err)
			}
		}
	})

	for _, attr := range []string{"export-ignore", "export-subst", "filter=lfs"} {
		t.Run(attr, func(t *testing.T) {
			files := map[string]string{
				"go.mod":             "module example.com/repo\n",
				"sub/.gitattributes": "*.bin " + attr + "\n",
				"sub/data.bin":       "data\n",
			}
			dir, hash := makeSourceRepo(t, files)
			for _, format := range []string{"tar.gz", "zip"} {
				err := convert(t, dir, format, hash)
				if !errors.Is(err, errNoGitHubArchive) || !strings.Contains(err.Error(), attr) {
					t.Errorf("%s: err = %v, want the %s attribute named", format, err, attr)
				}
			}
		})
	}
}

// fakeGitHub serves archives of dir the way github.com, codeload.github.com
// and proxy.pazer.ai do, and records every request it receives.
type fakeGitHub struct {
	dir        string
	status     map[string]int
	redirectTo string
	// wrongCommit is the source that serves the archive of commit other.
	wrongCommit string
	other       string
	// proxyRedirects makes the proxy pass the codeload redirect through.
	proxyRedirects bool

	mu       sync.Mutex
	requests []string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	record := req.Host + req.URL.Path
	if req.Host == "proxy.pazer.ai" {
		record = req.Host + "?url=" + req.URL.Query().Get("url")
	}
	f.mu.Lock()
	f.requests = append(f.requests, record)
	f.mu.Unlock()

	switch req.Host {
	case "github.com":
		format, name, found := archivePath(req.URL.Path)
		if !found {
			http.NotFound(w, req)
			return
		}
		if code := f.status["github."+format]; code != 0 {
			http.Error(w, "no archive", code)
			return
		}
		http.Redirect(w, req, "https://"+f.redirectTo+"/owner/repo/"+format+"/"+name, http.StatusFound)

	case "proxy.pazer.ai":
		inner, err := url.Parse(req.URL.Query().Get("url"))
		if err != nil || inner.Host != "github.com" {
			http.Error(w, "bad url", http.StatusBadRequest)
			return
		}
		format, name, found := archivePath(inner.Path)
		if !found {
			http.NotFound(w, req)
			return
		}
		if code := f.status["proxy."+format]; code != 0 {
			http.Error(w, "no archive", code)
			return
		}
		if f.proxyRedirects {
			http.Redirect(w, req, "https://140.82.112.10/owner/repo/"+format+"/"+name, http.StatusFound)
			return
		}
		f.serveArchive(w, req, "proxy", format, name)

	default:
		format, name := codeloadPath(req.URL.Path)
		f.serveArchive(w, req, "github", format, name)
	}
}

// archivePath splits "/owner/repo/archive/<name>.<format>".
func archivePath(urlPath string) (format, name string, found bool) {
	rest, found := strings.CutPrefix(urlPath, "/owner/repo/archive/")
	if !found {
		return "", "", false
	}
	if name, isTar := strings.CutSuffix(rest, ".tar.gz"); isTar {
		return "tar.gz", name, true
	}
	if name, isZip := strings.CutSuffix(rest, ".zip"); isZip {
		return "zip", name, true
	}
	return "", "", false
}

// codeloadPath splits "/owner/repo/<format>/<name>".
func codeloadPath(urlPath string) (format, name string) {
	format, name, _ = strings.Cut(strings.TrimPrefix(urlPath, "/owner/repo/"), "/")
	return format, name
}

func (f *fakeGitHub) serveArchive(w http.ResponseWriter, req *http.Request, via, format, name string) {
	if f.wrongCommit == via+"."+format {
		name = f.other
	}
	cmd := exec.Command("git", "archive", "--format="+format, "--prefix=repo-x/", name)
	cmd.Dir = f.dir
	served, err := cmd.Output()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	w.Write(served)
}

func TestGitHubArchiveFallback(t *testing.T) {
	testenv.MustHaveExecPath(t, "git")
	source, hash := makeSourceRepo(t, sourceFilesWithLink())
	other := gitIn(t, source, "commit-tree", "-m", "other", gitIn(t, source, "rev-parse", "HEAD^{tree}"))
	want := zipEntries(t, referenceZip(t, source, hash))

	const (
		githubTar   = "github.com/owner/repo/archive/refs/tags/v1.0.0.tar.gz"
		codeloadTar = "codeload.github.com/owner/repo/tar.gz/refs/tags/v1.0.0"
		proxyTar    = "proxy.pazer.ai?url=https://github.com/owner/repo/archive/refs/tags/v1.0.0.tar.gz"
		githubZip   = "github.com/owner/repo/archive/refs/tags/v1.0.0.zip"
		codeloadZip = "codeload.github.com/owner/repo/zip/refs/tags/v1.0.0"
		proxyZip    = "proxy.pazer.ai?url=https://github.com/owner/repo/archive/refs/tags/v1.0.0.zip"
	)
	missing := func(sources ...string) map[string]int {
		status := make(map[string]int)
		for _, source := range sources {
			status[source] = http.StatusNotFound
		}
		return status
	}

	cases := []struct {
		name           string
		status         map[string]int
		redirectTo     string
		wrongCommit    string
		proxyRedirects bool
		wantRequests   []string
		wantGit        bool
	}{
		{
			name:         "github.com tar.gz first",
			wantRequests: []string{githubTar, codeloadTar},
		},
		// web.Get asks again with GOAUTH credentials after a 4xx, so a
		// missing archive is requested twice.
		{
			name:         "proxy tar.gz when github.com has none",
			status:       missing("github.tar.gz"),
			wantRequests: []string{githubTar, githubTar, proxyTar},
		},
		{
			name:         "proxy tar.gz when the github.com one is of another commit",
			wrongCommit:  "github.tar.gz",
			wantRequests: []string{githubTar, codeloadTar, proxyTar},
		},
		{
			name:         "github.com zip when there is no tar.gz",
			status:       missing("github.tar.gz", "proxy.tar.gz"),
			wantRequests: []string{githubTar, githubTar, proxyTar, proxyTar, githubZip, codeloadZip},
		},
		{
			name:         "proxy zip after every other archive",
			status:       missing("github.tar.gz", "proxy.tar.gz", "github.zip"),
			wantRequests: []string{githubTar, githubTar, proxyTar, proxyTar, githubZip, githubZip, proxyZip},
		},
		{
			name:         "git when there is no archive",
			status:       missing("github.tar.gz", "proxy.tar.gz", "github.zip", "proxy.zip"),
			wantRequests: []string{githubTar, githubTar, proxyTar, proxyTar, githubZip, githubZip, proxyZip, proxyZip},
			wantGit:      true,
		},
		{
			name:           "git when every redirect leaves GitHub",
			redirectTo:     "evil.example",
			proxyRedirects: true,
			wantRequests:   []string{githubTar, proxyTar, githubZip, proxyZip},
			wantGit:        true,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeGitHub{
				dir:            source,
				status:         test.status,
				redirectTo:     test.redirectTo,
				wrongCommit:    test.wrongCommit,
				other:          other,
				proxyRedirects: test.proxyRedirects,
			}
			if fake.redirectTo == "" {
				fake.redirectTo = "codeload.github.com"
			}
			server := httptest.NewTLSServer(fake)
			defer server.Close()
			var hooks []intercept.Interceptor
			for _, host := range []string{"github.com", "codeload.github.com", "proxy.pazer.ai", "evil.example", "140.82.112.10"} {
				hooks = append(hooks, intercept.Interceptor{Scheme: "https", FromHost: host, ToHost: server.Listener.Addr().String(), Client: server.Client()})
			}
			defer intercept.AddTestHooks(hooks)()

			// Each case gets its own remote, so it gets its own work directory.
			remote := filepath.Join(t.TempDir(), "remote.git")
			gitIn(t, source, "clone", "-q", "--bare", source, remote)
			ctx := testContext(t)
			repo, err := newGitRepo(ctx, "file://"+filepath.ToSlash(remote), false)
			if err != nil {
				t.Fatal(err)
			}
			git := repo.(*gitRepo)
			git.github = &githubRepo{"owner", "repo"}

			info, err := repo.Stat(ctx, "v1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			if info.Name != hash || info.Version != "v1.0.0" || !info.Time.Equal(commitTime) {
				t.Errorf("Stat = %+v, want %s v1.0.0 at %v", info, hash, commitTime)
			}
			if info.Origin == nil || info.Origin.Ref != "refs/tags/v1.0.0" || info.Origin.Hash != hash {
				t.Errorf("Stat origin = %+v, want refs/tags/v1.0.0 at %s", info.Origin, hash)
			}

			_, statErr := git.runGit(ctx, "git", "cat-file", "-e", hash+"^{commit}")
			if hasCommit := statErr == nil; hasCommit != test.wantGit {
				t.Errorf("git fetched the commit: %v, want %v", hasCommit, test.wantGit)
			}

			gomod, err := repo.ReadFile(ctx, "v1.0.0", "go.mod", MaxGoMod)
			if err != nil || string(gomod) != sourceFiles["go.mod"] {
				t.Errorf("ReadFile(go.mod) = %q, %v, want %q", gomod, err, sourceFiles["go.mod"])
			}
			if _, err := repo.ReadFile(ctx, "v1.0.0", "missing.go", MaxGoMod); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("ReadFile(missing.go) err = %v, want fs.ErrNotExist", err)
			}

			for _, subdir := range []string{"", "sub"} {
				zipRC, err := repo.ReadZip(ctx, "v1.0.0", subdir, MaxZipFile)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(zipRC)
				zipRC.Close()
				if err != nil {
					t.Fatal(err)
				}
				wantHere := want
				if subdir != "" {
					wantHere = make(map[string]string)
					for name, body := range want {
						if strings.HasPrefix(name, archivePrefix+subdir+"/") {
							wantHere[name] = body
						}
					}
				}
				if got := zipEntries(t, data); !reflect.DeepEqual(got, wantHere) {
					t.Errorf("ReadZip(%q) differs from git archive:\ngot  %q\nwant %q", subdir, got, wantHere)
				}
			}
			if _, err := repo.ReadZip(ctx, "v1.0.0", "nowhere", MaxZipFile); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("ReadZip(nowhere) err = %v, want fs.ErrNotExist", err)
			}

			fake.mu.Lock()
			got := fake.requests
			fake.mu.Unlock()
			if !reflect.DeepEqual(got, test.wantRequests) {
				t.Errorf("requests:\ngot  %q\nwant %q", got, test.wantRequests)
			}
			if test.wantGit {
				return
			}

			// A new process finds the kept archive and fetches nothing.
			again, err := newGitRepo(ctx, "file://"+filepath.ToSlash(remote), false)
			if err != nil {
				t.Fatal(err)
			}
			again.(*gitRepo).github = &githubRepo{"owner", "repo"}
			if _, err := again.Stat(ctx, hash); err != nil {
				t.Fatal(err)
			}
			if gomod, err := again.ReadFile(ctx, hash, "go.mod", MaxGoMod); err != nil || string(gomod) != sourceFiles["go.mod"] {
				t.Errorf("ReadFile from the kept archive = %q, %v", gomod, err)
			}
			fake.mu.Lock()
			extra := fake.requests[len(test.wantRequests):]
			fake.mu.Unlock()
			if len(extra) != 0 {
				t.Errorf("the kept archive was downloaded again: %q", extra)
			}
		})
	}
}

// TestGitHubRecentTagWithoutHistory covers a pseudo-version base for a
// commit that came from an archive: git has no history for it.
func TestGitHubRecentTagWithoutHistory(t *testing.T) {
	source, _ := makeSourceRepo(t, sourceFiles)
	gitIn(t, source, "commit", "-q", "--allow-empty", "-m", "after the tag")
	head := gitIn(t, source, "rev-parse", "HEAD")

	fake := &fakeGitHub{dir: source, redirectTo: "codeload.github.com"}
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	var hooks []intercept.Interceptor
	for _, host := range []string{"github.com", "codeload.github.com"} {
		hooks = append(hooks, intercept.Interceptor{Scheme: "https", FromHost: host, ToHost: server.Listener.Addr().String(), Client: server.Client()})
	}
	defer intercept.AddTestHooks(hooks)()

	remote := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, source, "clone", "-q", "--bare", source, remote)
	ctx := testContext(t)
	repo, err := newGitRepo(ctx, "file://"+filepath.ToSlash(remote), false)
	if err != nil {
		t.Fatal(err)
	}
	repo.(*gitRepo).github = &githubRepo{"owner", "repo"}

	info, err := repo.Stat(ctx, "master")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != head {
		t.Fatalf("Stat(master) = %s, want %s", info.Name, head)
	}
	tag, err := repo.RecentTag(ctx, head, "", func(string) bool { return true })
	if err != nil || tag != "v1.0.0" {
		t.Errorf("RecentTag = %q, %v, want v1.0.0", tag, err)
	}
	tag, err = repo.RecentTag(ctx, head, "sub/", func(string) bool { return true })
	if err != nil || tag != "" {
		t.Errorf("RecentTag(sub/) = %q, %v, want no tag", tag, err)
	}
}
