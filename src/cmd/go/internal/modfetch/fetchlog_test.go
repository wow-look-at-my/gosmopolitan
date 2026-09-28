// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package modfetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"internal/testenv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"cmd/go/internal/modfetch/codehost"
	"cmd/go/internal/web"
	"cmd/go/internal/web/intercept"

	"golang.org/x/mod/module"
)

func TestFetchLine(t *testing.T) {
	cases := []struct {
		name     string
		route    string
		size     int64
		transfer time.Duration
		total    time.Duration
		failed   bool
		want     string
	}{
		{
			name: "bytes", route: "module proxy https://proxy.golang.org", size: 512, transfer: 250 * time.Millisecond, total: time.Second,
			want: "go: downloading m v1.0.0: module proxy https://proxy.golang.org, 512 B in 0.25s (2.0 kB/s), 1.00s total",
		},
		{
			name: "megabytes", route: "tar.gz archive", size: 1_200_000, transfer: 400 * time.Millisecond, total: 620 * time.Millisecond,
			want: "go: downloading m v1.0.0: tar.gz archive, 1.2 MB in 0.40s (3.0 MB/s), 0.62s total",
		},
		{
			name: "gigabytes", route: "git", size: 2_500_000_000, transfer: 10 * time.Second, total: 12 * time.Second,
			want: "go: downloading m v1.0.0: git, 2.5 GB in 10.00s (250.0 MB/s), 12.00s total",
		},
		{
			name: "zero duration has no speed", route: "zip archive", size: 1500, total: time.Millisecond,
			want: "go: downloading m v1.0.0: zip archive, 1.5 kB in 0.00s, 0.00s total",
		},
		{
			name: "nothing transferred", route: "tar.gz archive", total: 30 * time.Millisecond,
			want: "go: downloading m v1.0.0: tar.gz archive, nothing transferred, 0.03s total",
		},
		{
			name: "failed", route: "git", size: 10, transfer: time.Second, total: 2 * time.Second, failed: true,
			want: "go: downloading m v1.0.0: failed, 2.00s total",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := fetchLine("m v1.0.0", test.route, test.size, test.transfer, test.total, test.failed)
			if got != test.want {
				t.Errorf("fetchLine =\n\t%q\nwant\n\t%q", got, test.want)
			}
		})
	}
}

func TestModuleName(t *testing.T) {
	for mod, want := range map[module.Version]string{
		{Path: "rsc.io/quote", Version: "v1.5.2"}:                               "rsc.io/quote v1.5.2",
		{Path: "golang.org/toolchain", Version: "v0.0.1-go1.13.1.darwin-amd64"}: "go1.13.1 (darwin/amd64)",
	} {
		if got := moduleName(mod); got != want {
			t.Errorf("moduleName(%v) = %q, want %q", mod, got, want)
		}
	}
}

// captureFetchLog sends report lines to a buffer for the rest of the test.
func captureFetchLog(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	previous := fetchLog
	fetchLog = &buf
	t.Cleanup(func() { fetchLog = previous })
	return &buf
}

// reportLines splits the captured log into lines.
func reportLines(buf *bytes.Buffer) []string {
	text := strings.TrimSuffix(buf.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func TestFetchReportPrintsOnce(t *testing.T) {
	buf := captureFetchLog(t)
	mod := module.Version{Path: "example.com/m", Version: "v1.0.0"}

	cached := newFetchReport(mod)
	cached.finish(nil)
	if lines := reportLines(buf); len(lines) != 0 {
		t.Fatalf("a fetch with no route printed %q", lines)
	}

	fetched := newFetchReport(mod)
	fetched.rec.SetRoute("git")
	fetched.rec.AddTransfer(time.Now(), 2000, time.Millisecond)
	fetched.finish(nil)
	fetched.finish(nil)
	fetched.finish(errors.New("late"))
	lines := reportLines(buf)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "go: downloading example.com/m v1.0.0: git, 2.0 kB in ") {
		t.Fatalf("report printed %q, want one line for git", lines)
	}

	buf.Reset()
	failed := newFetchReport(mod)
	failed.finish(errors.New("no such module"))
	if lines := reportLines(buf); len(lines) != 1 || !strings.HasPrefix(lines[0], "go: downloading example.com/m v1.0.0: failed, ") {
		t.Fatalf("failed fetch printed %q, want one failure line", lines)
	}

	// A transfer that Stat ran before the download began counts toward the total, up to the time it took.
	buf.Reset()
	early := newFetchReport(mod)
	early.rec.SetRoute("tar.gz archive")
	early.rec.AddTransfer(early.start.Add(-time.Hour), 1000, 3*time.Second)
	early.finish(nil)
	if lines := reportLines(buf); len(lines) != 1 || !strings.Contains(lines[0], ", 1.0 kB in 3.00s (333 B/s), 3.") {
		t.Fatalf("early transfer printed %q, want 3s of transfer in the total", lines)
	}
}

func TestFetchReportNamesEarlierFailures(t *testing.T) {
	buf := captureFetchLog(t)
	report := newFetchReport(module.Version{Path: "example.com/m", Version: "v1.0.0"})
	report.rec.AddFailure("proxy.golang.org", &module.ModuleError{Path: "example.com/m", Err: &web.HTTPError{Status: "404 Not Found", StatusCode: 404}})
	report.rec.AddFailure("github.com tar.gz", fmt.Errorf("https://github.com/o/n/archive/x.tar.gz: %w", errors.New("archive holds no files")))
	report.rec.SetRoute("zip archive")
	report.rec.AddTransfer(time.Now(), 1000, time.Second)
	report.finish(nil)
	want := "go: downloading example.com/m v1.0.0: zip archive, because proxy.golang.org: 404 Not Found; github.com tar.gz: archive holds no files, 1.0 kB in 1.00s (1.0 kB/s), "
	if lines := reportLines(buf); len(lines) != 1 || !strings.HasPrefix(lines[0], want) {
		t.Fatalf("report printed %q, want one line starting %q", lines, want)
	}
	if name, isProxy := proxyName("https://proxy.golang.org"); !isProxy || name != "proxy.golang.org" {
		t.Errorf("proxyName(https://proxy.golang.org) = %q, %v", name, isProxy)
	}
	for _, entry := range []string{"direct", "noproxy", "off"} {
		if _, isProxy := proxyName(entry); isProxy {
			t.Errorf("proxyName(%q) reports a module proxy", entry)
		}
	}
}

// oneLine is the report of a fetch that transferred data: route, size, time,
// speed and total.
func oneLine(name, route string) *regexp.Regexp {
	return regexp.MustCompile(`^go: downloading ` + regexp.QuoteMeta(name+": "+route) +
		`, [0-9.]+ [kMG]?B in [0-9]+\.[0-9]{2}s \([0-9.]+ [kMG]?B/s\), [0-9]+\.[0-9]{2}s total$`)
}

// serveTLS routes host to handler for the rest of the test.
func serveTLS(t *testing.T, handler http.Handler, hosts ...string) {
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	var hooks []intercept.Interceptor
	for _, host := range hosts {
		hooks = append(hooks, intercept.Interceptor{Scheme: "https", FromHost: host, ToHost: server.Listener.Addr().String(), Client: server.Client()})
	}
	t.Cleanup(intercept.AddTestHooks(hooks))
}

func TestFetchReportThroughProxy(t *testing.T) {
	buf := captureFetchLog(t)
	body := bytes.Repeat([]byte("z"), 4096)
	serveTLS(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/example.com/m/@v/v1.0.0.zip" {
			http.NotFound(w, req)
			return
		}
		w.Write(body)
	}), "proxy.example")

	mod := module.Version{Path: "example.com/m", Version: "v1.0.0"}
	repo, err := newProxyRepo("https://proxy.example", mod.Path)
	if err != nil {
		t.Fatal(err)
	}
	report := newFetchReport(mod)
	ctx := codehost.WithFetch(context.Background(), report.rec)
	if err := repo.Zip(ctx, io.Discard, mod.Version); err != nil {
		t.Fatal(err)
	}
	report.finish(nil)

	lines := reportLines(buf)
	if len(lines) != 1 || !oneLine("example.com/m v1.0.0", "module proxy https://proxy.example").MatchString(lines[0]) || !strings.Contains(lines[0], ", 4.1 kB in ") {
		t.Fatalf("proxy fetch printed %q, want one line naming the proxy and 4.1 kB", lines)
	}
}

// fakeArchiveHost serves a git repository as github.com serves
// github.com/owner/repo: its ref advertisement and its archives.
type fakeArchiveHost struct {
	dir string
}

func (host fakeArchiveHost) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/owner/repo.git/info/refs" {
		refs, err := exec.Command("git", "upload-pack", "--advertise-refs", host.dir).Output()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		service := "# service=git-upload-pack\n"
		fmt.Fprintf(w, "%04x%s0000", len(service)+4, service)
		w.Write(refs)
		return
	}
	name, isTar := strings.CutSuffix(strings.TrimPrefix(req.URL.Path, "/owner/repo/archive/"), ".tar.gz")
	if !isTar || name == req.URL.Path {
		http.NotFound(w, req)
		return
	}
	cmd := exec.Command("git", "archive", "--format=tar.gz", "--prefix=repo-x/", name)
	cmd.Dir = host.dir
	served, err := cmd.Output()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	w.Write(served)
}

func TestDownloadReportsOneLine(t *testing.T) {
	testenv.MustHaveExecPath(t, "git")
	buf := captureFetchLog(t)

	source := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = source
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "-q", "--object-format=sha1", "--initial-branch=master")
	files := map[string]string{
		"go.mod":  "module github.com/owner/repo\n\ngo 1.20\n",
		"repo.go": "package repo\n\nconst Name = \"" + strings.Repeat("x", 3000) + "\"\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "files")
	runGit("tag", "v1.0.0")
	serveTLS(t, fakeArchiveHost{dir: source}, "github.com", "codeload.github.com")

	fetcher := NewFetcher()
	mod := module.Version{Path: "github.com/owner/repo", Version: "v1.0.0"}
	dir, err := fetcher.Download(context.Background(), mod)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "repo.go")); err != nil {
		t.Fatal(err)
	}
	lines := reportLines(buf)
	if len(lines) != 1 || !oneLine("github.com/owner/repo v1.0.0", "tar.gz archive").MatchString(lines[0]) {
		t.Fatalf("download printed %q, want one line for the tar.gz archive", lines)
	}

	// The module is in the cache now, so a second download prints nothing.
	buf.Reset()
	if _, err := NewFetcher().Download(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
	if lines := reportLines(buf); len(lines) != 0 {
		t.Fatalf("a cached module printed %q", lines)
	}
}
