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

func TestGitHubArchiveURL(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	repo := githubRepo{"wow-look-at-my", "slopfix"}
	cases := []struct{ ref, ext, want string }{
		{"refs/heads/master", ".tar.gz", "https://github.com/wow-look-at-my/slopfix/archive/refs/heads/master.tar.gz"},
		{"refs/tags/v1.2.3", ".zip", "https://github.com/wow-look-at-my/slopfix/archive/refs/tags/v1.2.3.zip"},
		{"refs/tags/sub/v1.2.3", ".tar.gz", "https://github.com/wow-look-at-my/slopfix/archive/refs/tags/sub/v1.2.3.tar.gz"},
		{"HEAD", ".tar.gz", "https://github.com/wow-look-at-my/slopfix/archive/" + hash + ".tar.gz"},
	}
	for _, test := range cases {
		if got := repo.archiveURL(test.ref, hash, test.ext); got != test.want {
			t.Errorf("archiveURL(%q, %q) = %q, want %q", test.ref, test.ext, got, test.want)
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

// fakeGitHub serves archives of dir the way github.com and
// codeload.github.com do, and records every request it receives.
type fakeGitHub struct {
	dir        string
	status     map[string]int
	redirectTo string
	rewrite    func(ext string, served []byte) []byte

	mu       sync.Mutex
	requests []string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, req.Host+req.URL.Path)
	f.mu.Unlock()

	if req.Host == "github.com" {
		rest, found := strings.CutPrefix(req.URL.Path, "/owner/repo/archive/")
		if !found {
			http.NotFound(w, req)
			return
		}
		ext := ".zip"
		name, isTar := strings.CutSuffix(rest, ".tar.gz")
		if isTar {
			ext = ".tar.gz"
		} else {
			name = strings.TrimSuffix(rest, ".zip")
		}
		if code := f.status[ext]; code != 0 {
			http.Error(w, "no archive", code)
			return
		}
		http.Redirect(w, req, "https://"+f.redirectTo+"/owner/repo/"+strings.TrimPrefix(ext, ".")+"/"+name, http.StatusFound)
		return
	}

	rest := strings.TrimPrefix(req.URL.Path, "/owner/repo/")
	format, name, _ := strings.Cut(rest, "/")
	ext := "." + format
	cmd := exec.Command("git", "archive", "--format="+format, "--prefix=repo-x/", name)
	cmd.Dir = f.dir
	served, err := cmd.Output()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	if f.rewrite != nil {
		served = f.rewrite(ext, served)
	}
	w.Write(served)
}

func TestGitHubArchiveFallback(t *testing.T) {
	testenv.MustHaveExecPath(t, "git")
	source, hash := makeSourceRepo(t, sourceFilesWithLink())
	other := gitIn(t, source, "commit-tree", "-m", "other", gitIn(t, source, "rev-parse", "HEAD^{tree}"))
	want := zipEntries(t, referenceZip(t, source, hash))

	cases := []struct {
		name         string
		status       map[string]int
		redirectTo   string
		wrongCommit  string // extension served as the archive of another commit
		wantRequests []string
		wantGit      bool
	}{
		{
			name: "tar.gz first",
			wantRequests: []string{
				"github.com/owner/repo/archive/refs/tags/v1.0.0.tar.gz",
				"codeload.github.com/owner/repo/tar.gz/refs/tags/v1.0.0",
			},
		},
		{
			name:   "zip when there is no tar.gz",
			status: map[string]int{".tar.gz": http.StatusNotFound},
			wantRequests: []string{
				"github.com/owner/repo/archive/refs/tags/v1.0.0.tar.gz",
				"github.com/owner/repo/archive/refs/tags/v1.0.0.zip",
				"codeload.github.com/owner/repo/zip/refs/tags/v1.0.0",
			},
		},
		{
			name:        "zip when the tar.gz is of another commit",
			wrongCommit: ".tar.gz",
			wantRequests: []string{
				"github.com/owner/repo/archive/refs/tags/v1.0.0.tar.gz",
				"codeload.github.com/owner/repo/tar.gz/refs/tags/v1.0.0",
				"github.com/owner/repo/archive/refs/tags/v1.0.0.zip",
				"codeload.github.com/owner/repo/zip/refs/tags/v1.0.0",
			},
		},
		{
			name:   "git when there is no archive",
			status: map[string]int{".tar.gz": http.StatusNotFound, ".zip": http.StatusNotFound},
			wantRequests: []string{
				"github.com/owner/repo/archive/refs/tags/v1.0.0.tar.gz",
				"github.com/owner/repo/archive/refs/tags/v1.0.0.zip",
			},
			wantGit: true,
		},
		{
			name:       "git when github.com redirects off GitHub",
			redirectTo: "evil.example",
			wantRequests: []string{
				"github.com/owner/repo/archive/refs/tags/v1.0.0.tar.gz",
				"github.com/owner/repo/archive/refs/tags/v1.0.0.zip",
			},
			wantGit: true,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeGitHub{dir: source, status: test.status, redirectTo: test.redirectTo}
			if fake.redirectTo == "" {
				fake.redirectTo = "codeload.github.com"
			}
			if test.wrongCommit != "" {
				fake.rewrite = func(ext string, served []byte) []byte {
					if ext != test.wrongCommit {
						return served
					}
					cmd := exec.Command("git", "archive", "--format="+strings.TrimPrefix(ext, "."), "--prefix=repo-x/", other)
					cmd.Dir = source
					out, err := cmd.Output()
					if err != nil {
						panic(err)
					}
					return out
				}
			}
			server := httptest.NewTLSServer(fake)
			defer server.Close()
			var hooks []intercept.Interceptor
			for _, host := range []string{"github.com", "codeload.github.com", "evil.example"} {
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
