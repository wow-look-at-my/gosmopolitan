// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codehost

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"internal/testenv"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
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
			// No hash goes in, so the commit must come from the archive.
			entries, when, commit, err := parseArchive(served, "."+format, "")
			if err != nil {
				t.Fatal(err)
			}
			if commit != hash {
				t.Errorf("commit = %q, want %s", commit, hash)
			}
			if !when.Equal(commitTime) {
				t.Errorf("commit time = %v, want %v", when, commitTime)
			}
			got := make(map[string]string)
			for _, entry := range entries {
				body := string(entry.data)
				if entry.mode&os.ModeSymlink != 0 {
					body = "symlink:" + body
				}
				got[archivePrefix+entry.name] = body
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("parsed archive differs from git archive:\ngot  %q\nwant %q", got, want)
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
	// proxyRedirects makes the proxy pass the codeload redirect through.
	proxyRedirects bool

	// infoRefsStatus fails every info/refs request, direct or proxied.
	infoRefsStatus int
	// apiStatus fails every direct api.github.com request with this code.
	apiStatus int

	mu sync.Mutex
	// requests records archive traffic, and refRequests the ref listings: info/refs, github-state-mirror and the API.
	requests    []string
	refRequests []string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	record := req.Host + req.URL.Path
	target := req.URL
	if req.Host == "proxy.pazer.ai" {
		record = req.Host + "?url=" + req.URL.Query().Get("url")
		inner, err := url.Parse(req.URL.Query().Get("url"))
		if err != nil {
			http.Error(w, "bad url", http.StatusBadRequest)
			return
		}
		target = inner
	} else {
		target = &url.URL{Host: req.Host, Path: req.URL.Path, RawQuery: req.URL.RawQuery}
	}
	isInfoRefs := target.Host == "github.com" && strings.HasSuffix(target.Path, "/info/refs")
	isAPI := target.Host == "api.github.com" || req.Host == gsmHost
	f.mu.Lock()
	if isAPI || isInfoRefs {
		f.refRequests = append(f.refRequests, record)
	} else {
		f.requests = append(f.requests, record)
	}
	f.mu.Unlock()

	if req.Host == gsmHost {
		http.Error(w, "unauthorized: missing Authorization header", http.StatusUnauthorized)
		return
	}
	if isInfoRefs {
		if f.infoRefsStatus != 0 {
			http.Error(w, "no refs", f.infoRefsStatus)
			return
		}
		cmd := exec.Command("git", "upload-pack", "--advertise-refs", f.dir)
		refs, err := cmd.Output()
		if err != nil {
			panic(err)
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		service := "# service=git-upload-pack\n"
		fmt.Fprintf(w, "%04x%s0000", len(service)+4, service)
		w.Write(refs)
		return
	}
	if isAPI {
		if req.Host != "proxy.pazer.ai" && f.apiStatus != 0 {
			http.Error(w, "rate limited", f.apiStatus)
			return
		}
		f.serveAPI(w, req, target)
		return
	}
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

// serveAPI answers the api.github.com requests that githubRefs makes, from
// the refs of the source repository.
func (f *fakeGitHub) serveAPI(w http.ResponseWriter, req *http.Request, apiURL *url.URL) {
	type named struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	list := func(prefix string) []named {
		cmd := exec.Command("git", "for-each-ref", "--format=%(refname:strip=2) %(if)%(*objectname)%(then)%(*objectname)%(else)%(objectname)%(end)", prefix)
		cmd.Dir = f.dir
		out, err := cmd.Output()
		if err != nil {
			panic(err)
		}
		items := []named{}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			name, sha, found := strings.Cut(line, " ")
			if !found {
				continue
			}
			item := named{Name: name}
			item.Commit.SHA = sha
			items = append(items, item)
		}
		return items
	}
	var body any
	page := apiURL.Query().Get("page")
	switch apiURL.Path {
	case "/repos/owner/repo":
		body = map[string]string{"default_branch": "master"}
	case "/repos/owner/repo/tags":
		body = []named{}
		if page == "1" {
			body = list("refs/tags")
		}
	case "/repos/owner/repo/branches":
		body = []named{}
		if page == "1" {
			body = list("refs/heads")
		}
	default:
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
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
	cmd := exec.Command("git", "archive", "--format="+format, "--prefix=repo-x/", name)
	cmd.Dir = f.dir
	served, err := cmd.Output()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	w.Write(served)
}

// serveFakeGitHub routes every host the fetcher can reach to fake, and a few
// it must never reach, so a request to those shows up in fake.requests.
func serveFakeGitHub(t *testing.T, fake *fakeGitHub) {
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)
	var hooks []intercept.Interceptor
	for _, host := range []string{"github.com", "codeload.github.com", "api.github.com", gsmHost, "proxy.pazer.ai", "evil.example", "140.82.112.10"} {
		hooks = append(hooks, intercept.Interceptor{Scheme: "https", FromHost: host, ToHost: server.Listener.Addr().String(), Client: server.Client()})
	}
	t.Cleanup(intercept.AddTestHooks(hooks))
}

// TestGitHubRefsOverHTTP resolves tags, branches and HEAD with no git remote
// behind the repository, so every answer has to come over plain HTTP.
func TestGitHubRefsOverHTTP(t *testing.T) {
	source, tagged := makeSourceRepo(t, sourceFiles)
	gitIn(t, source, "tag", "-a", "-m", "annotated", "v1.1.0")
	gitIn(t, source, "commit", "-q", "--allow-empty", "-m", "after the tag")
	head := gitIn(t, source, "rev-parse", "HEAD")

	cases := []struct {
		name           string
		infoRefsStatus int
		apiStatus      int
		// wantFrom is where the refs came from: a prefix of the request that served them.
		wantFrom string
	}{
		{name: "info/refs", wantFrom: "github.com/owner/repo.git/info/refs"},
		{name: "api", infoRefsStatus: http.StatusForbidden, wantFrom: "api.github.com/repos/owner/repo"},
		{name: "api through the proxy", infoRefsStatus: http.StatusForbidden, apiStatus: http.StatusForbidden, wantFrom: "proxy.pazer.ai?url=https://api.github.com/"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeGitHub{dir: source, redirectTo: "codeload.github.com", infoRefsStatus: test.infoRefsStatus, apiStatus: test.apiStatus}
			serveFakeGitHub(t, fake)
			ctx := testContext(t)
			repo := fakeGitHubRepo(t, ctx, filepath.Join(t.TempDir(), "no-such-remote.git"))

			tags, err := repo.Tags(ctx, "v")
			if err != nil {
				t.Fatal(err)
			}
			if want := []Tag{{"v1.0.0", tagged}, {"v1.1.0", tagged}}; !reflect.DeepEqual(tags.List, want) {
				t.Errorf("Tags = %v, want %v", tags.List, want)
			}
			latest, err := repo.Latest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if latest.Name != head || latest.Origin.Ref != "HEAD" {
				t.Errorf("Latest = %s at %q, want %s at HEAD", latest.Name, latest.Origin.Ref, head)
			}
			master, err := repo.Stat(ctx, "master")
			if err != nil {
				t.Fatal(err)
			}
			if master.Name != head || master.Origin.Ref != "refs/heads/master" {
				t.Errorf("Stat(master) = %s at %q, want %s at refs/heads/master", master.Name, master.Origin.Ref, head)
			}

			fake.mu.Lock()
			defer fake.mu.Unlock()
			// Refs load once, so the last ref request is the one that answered.
			last := fake.refRequests[len(fake.refRequests)-1]
			if !strings.HasPrefix(last, test.wantFrom) {
				t.Errorf("refs came from %s, want %s\nall: %q", last, test.wantFrom, fake.refRequests)
			}
			// github-state-mirror comes before api.github.com whenever the API runs.
			usedAPI := test.infoRefsStatus != 0
			sawGSM := slices.ContainsFunc(fake.refRequests, func(request string) bool { return strings.HasPrefix(request, gsmHost) })
			if sawGSM != usedAPI {
				t.Errorf("asked github-state-mirror: %v, want %v\nall: %q", sawGSM, usedAPI, fake.refRequests)
			}
		})
	}
}

// fakeGitHubRepo opens the local bare repository at dir as if it were
// github.com/owner/repo.
func fakeGitHubRepo(t *testing.T, ctx context.Context, dir string) *gitRepo {
	t.Helper()
	remote := "file://" + filepath.ToSlash(dir)
	previous := githubRemote
	githubRemote = func(name string) (githubRepo, bool) {
		if name == remote {
			return githubRepo{"owner", "repo"}, true
		}
		return previous(name)
	}
	t.Cleanup(func() { githubRemote = previous })
	repo, err := newGitRepo(ctx, remote, false)
	if err != nil {
		t.Fatal(err)
	}
	return repo.(*gitRepo)
}

func TestGitHubArchiveFallback(t *testing.T) {
	testenv.MustHaveExecPath(t, "git")
	source, hash := makeSourceRepo(t, sourceFilesWithLink())
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
		proxyRedirects bool
		wantRequests   []string
		wantGit        bool
	}{
		{
			name:         "github.com tar.gz first",
			wantRequests: []string{githubTar, codeloadTar},
		},
		// web.Get asks github.com again with GOAUTH credentials after a 4xx.
		// A proxied request carries its credential from the start, so it
		// is sent once.
		{
			name:         "proxy tar.gz when github.com has none",
			status:       missing("github.tar.gz"),
			wantRequests: []string{githubTar, githubTar, proxyTar},
		},
		{
			name:         "github.com zip when there is no tar.gz",
			status:       missing("github.tar.gz", "proxy.tar.gz"),
			wantRequests: []string{githubTar, githubTar, proxyTar, githubZip, codeloadZip},
		},
		{
			name:         "proxy zip after every other archive",
			status:       missing("github.tar.gz", "proxy.tar.gz", "github.zip"),
			wantRequests: []string{githubTar, githubTar, proxyTar, githubZip, githubZip, proxyZip},
		},
		{
			name:         "git when there is no archive",
			status:       missing("github.tar.gz", "proxy.tar.gz", "github.zip", "proxy.zip"),
			wantRequests: []string{githubTar, githubTar, proxyTar, githubZip, githubZip, proxyZip},
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
				proxyRedirects: test.proxyRedirects,
			}
			if fake.redirectTo == "" {
				fake.redirectTo = "codeload.github.com"
			}
			serveFakeGitHub(t, fake)

			// Each case gets its own remote, so it gets its own work directory.
			remote := filepath.Join(t.TempDir(), "remote.git")
			gitIn(t, source, "clone", "-q", "--bare", source, remote)
			ctx := testContext(t)
			git := fakeGitHubRepo(t, ctx, remote)
			repo := Repo(git)

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

			if loadedRefs := git.refs != nil; loadedRefs != test.wantGit {
				t.Errorf("listed refs: %v, want %v", loadedRefs, test.wantGit)
			}
			// No git ran unless every archive failed. gitDirReady runs none.
			if madeGitDir := git.gitDirReady(); madeGitDir != test.wantGit {
				t.Errorf("made a git repository: %v, want %v", madeGitDir, test.wantGit)
			}

			gomod, err := repo.ReadFile(ctx, "v1.0.0", "go.mod", MaxGoMod)
			if err != nil || string(gomod) != sourceFiles["go.mod"] {
				t.Errorf("ReadFile(go.mod) = %q, %v, want %q", gomod, err, sourceFiles["go.mod"])
			}
			if _, err := repo.ReadFile(ctx, "v1.0.0", "missing.go", MaxGoMod); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("ReadFile(missing.go) err = %v, want fs.ErrNotExist", err)
			}

			for _, subdir := range []string{"", "sub"} {
				var got map[string]string
				if test.wantGit {
					zipRC, err := repo.ReadZip(ctx, "v1.0.0", subdir, MaxZipFile)
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(zipRC)
					zipRC.Close()
					if err != nil {
						t.Fatal(err)
					}
					got = zipEntries(t, data)
				} else {
					// A commit from an archive is never made into a zip.
					if _, err := repo.ReadZip(ctx, "v1.0.0", subdir, MaxZipFile); !errors.Is(err, errors.ErrUnsupported) {
						t.Errorf("ReadZip(%q) err = %v, want errors.ErrUnsupported", subdir, err)
					}
					files, err := git.ReadFiles(ctx, "v1.0.0", subdir)
					if err != nil {
						t.Fatal(err)
					}
					got = make(map[string]string)
					for _, file := range files {
						name := archivePrefix + file.Name
						if subdir != "" {
							name = archivePrefix + subdir + "/" + file.Name
						}
						body := string(file.Data)
						if file.Mode&os.ModeSymlink != 0 {
							body = "symlink:" + body
						}
						got[name] = body
					}
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
				if !reflect.DeepEqual(got, wantHere) {
					t.Errorf("files of %q differ from git archive:\ngot  %q\nwant %q", subdir, got, wantHere)
				}
			}
			if !test.wantGit {
				if _, err := git.ReadFiles(ctx, "v1.0.0", "nowhere"); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("ReadFiles(nowhere) err = %v, want fs.ErrNotExist", err)
				}
				// Only the archive as served is on disk. A zip is there only when GitHub served one.
				_, tarErr := os.Stat(git.githubArchivePath(hash, ".tar.gz"))
				err := filepath.WalkDir(git.dir, func(name string, entry fs.DirEntry, err error) error {
					if err != nil || !strings.HasSuffix(name, ".zip") {
						return err
					}
					if tarErr == nil || name != git.githubArchivePath(hash, ".zip") {
						t.Errorf("found a zip: %s", name)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
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
			again := fakeGitHubRepo(t, ctx, remote)
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
			if again.gitDirReady() {
				t.Errorf("serving from archives made a git repository")
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
	serveFakeGitHub(t, fake)

	remote := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, source, "clone", "-q", "--bare", source, remote)
	ctx := testContext(t)
	repo := fakeGitHubRepo(t, ctx, remote)

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
