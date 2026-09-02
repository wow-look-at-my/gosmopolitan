// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

// fakeCacheServer answers the client's single-object endpoints and refuses the
// batch and index ones, which is a shape the client is required to handle: an
// older server. That keeps the fake small while still exercising the real wire
// path -- HTTP, basic auth, lz4 framing, the outputid header and the guards.
type fakeCacheServer struct {
	mu      sync.Mutex
	objects map[string][]byte      // key -> lz4-framed body
	meta    map[string]http.Header // key -> every X-Cache-Meta-* header sent on its PUT
	puts    int
	gets    int

	// failPuts refuses every upload, which is what an unwell server does and
	// what drives the client's HTTP error summaries.
	failPuts bool
}

func newFakeCacheServer(t *testing.T) (*fakeCacheServer, *httptest.Server) {
	t.Helper()
	f := &fakeCacheServer{objects: map[string][]byte{}, meta: map[string]http.Header{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeCacheServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if user, pass, ok := r.BasicAuth(); !ok || user != "u" || pass != "p" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// No index and no batch endpoints: the client must fall back to fetching
	// and storing one object at a time.
	if strings.HasSuffix(r.URL.Path, "/_index") || strings.Contains(r.URL.Path, "/_batch/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	key := r.URL.Path
	switch r.Method {
	case http.MethodPut:
		if f.failPuts {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		f.objects[key] = body
		f.meta[key] = r.Header.Clone()
		f.puts++
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		f.mu.Lock()
		body, ok := f.objects[key]
		meta := f.meta[key]
		if ok {
			f.gets++
		}
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Generic passthrough, matching the real server: every X-Cache-Meta-*
		// header sent on the PUT is reflected back on the GET.
		for name, vals := range meta {
			if strings.HasPrefix(strings.ToLower(name), "x-cache-meta-") {
				w.Header().Set(name, vals[0])
			}
		}
		w.Write(body)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeCacheServer) stored() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objects)
}

// configureShared points the client at srv for the duration of the test.
func configureShared(t *testing.T, srv *httptest.Server) {
	t.Helper()
	cfg := fmt.Sprintf(`{"endpoint":%q,"bucket":"b","username":"u","password":"p"}`, srv.URL)
	t.Setenv(cacheclient.ConfigEnv, base64.StdEncoding.EncodeToString([]byte(cfg)))
}

// openShared builds a shared cache over a fresh disk cache in dir.
func openShared(t *testing.T, dir string) Cache {
	t.Helper()
	disk, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	c := newSharedCache(disk)
	if c == nil {
		t.Fatal("newSharedCache returned nil with a shared cache configured")
	}
	return c
}

func testActionID(seed string) ActionID {
	return ActionID(sha256.Sum256([]byte("action:" + seed)))
}

// A build whose local cache is empty must be able to get its output from the
// shared tier, with no cache program on PATH and no subprocess: this is the
// whole point of linking the client in. The body has to arrive on disk too,
// because what the compiler is handed is a path.
func TestSharedCache_SecondBuildGetsOutputOverTheNetwork(t *testing.T) {
	f, srv := newFakeCacheServer(t)
	configureShared(t, srv)

	body := []byte("export data the second build must not have to recompute")
	id := testActionID("shared-round-trip")

	first := openShared(t, t.TempDir())
	outputID, size, err := first.Put(id, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if size != int64(len(body)) {
		t.Fatalf("Put stored %d bytes, want %d", size, len(body))
	}
	// Close drains the upload; until then it may still be in flight.
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if f.stored() == 0 {
		t.Fatal("nothing reached the shared cache")
	}

	// A different machine: same shared cache, empty local cache.
	second := openShared(t, t.TempDir())
	defer second.Close()
	entry, err := second.Get(id)
	if err != nil {
		t.Fatalf("Get after a cold local cache: %v", err)
	}
	if entry.OutputID != outputID {
		t.Fatalf("Get returned outputID %x, want %x", entry.OutputID, outputID)
	}
	if entry.Size != int64(len(body)) {
		t.Fatalf("Get reported size %d, want %d", entry.Size, len(body))
	}

	// The contract every caller relies on: the entry names a readable file.
	got, err := readOutputFile(second, entry.OutputID)
	if err != nil {
		t.Fatalf("reading the file the entry names: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("the shared cache served %q, want %q", got, body)
	}
}

// A network hit must restore exactly the mode the original PutExecutable (or
// Put) call chose -- never a guess from content, never a blanket +x. The
// build system execs some cached outputs directly (go run, a shebang
// script), and the choice of Put vs PutExecutable already carries that
// decision; this pins that it survives the round trip through the wire's
// executable metadata instead of being lost or defaulted.
func TestSharedCache_NetworkHitRestoresExecutableBit(t *testing.T) {
	f, srv := newFakeCacheServer(t)
	configureShared(t, srv)

	exeBody := []byte("#!/bin/sh\necho hi\n")
	exeID := testActionID("network-hit-executable")
	plainBody := []byte("ordinary compiled package output")
	plainID := testActionID("network-hit-plain")

	first := openShared(t, t.TempDir())
	exeCache, ok := first.(ExecutableCache)
	if !ok {
		t.Fatalf("%T does not implement ExecutableCache", first)
	}
	if _, _, err := exeCache.PutExecutable(exeID, "cached-script", bytes.NewReader(exeBody)); err != nil {
		t.Fatalf("PutExecutable: %v", err)
	}
	if _, _, err := first.Put(plainID, bytes.NewReader(plainBody)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if f.stored() != 2 {
		t.Fatalf("stored %d objects, want 2", f.stored())
	}

	second := openShared(t, t.TempDir())
	defer second.Close()

	exeEntry, err := second.Get(exeID)
	if err != nil {
		t.Fatalf("Get executable after a cold local cache: %v", err)
	}
	exeName := second.OutputFile(exeEntry.OutputID)
	info, err := os.Stat(exeName)
	if err != nil {
		t.Fatalf("Stat(%s): %v", exeName, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("network hit for a PutExecutable object %s has mode %v, want an executable bit set", exeName, info.Mode())
	}

	plainEntry, err := second.Get(plainID)
	if err != nil {
		t.Fatalf("Get plain object after a cold local cache: %v", err)
	}
	plainName := second.OutputFile(plainEntry.OutputID)
	info, err = os.Stat(plainName)
	if err != nil {
		t.Fatalf("Stat(%s): %v", plainName, err)
	}
	if info.Mode()&0o111 != 0 {
		t.Fatalf("network hit for an ordinary Put object %s has mode %v, want no executable bit", plainName, info.Mode())
	}
}

// A local hit must not touch the network: the disk cache stays the fast path.
func TestSharedCache_LocalHitSkipsTheNetwork(t *testing.T) {
	f, srv := newFakeCacheServer(t)
	configureShared(t, srv)

	dir := t.TempDir()
	c := openShared(t, dir)
	defer c.Close()

	body := []byte("stored locally")
	id := testActionID("local-hit")
	if _, _, err := c.Put(id, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	f.mu.Lock()
	f.gets = 0
	f.mu.Unlock()

	if _, err := c.Get(id); err != nil {
		t.Fatalf("Get: %v", err)
	}

	f.mu.Lock()
	gets := f.gets
	f.mu.Unlock()
	if gets != 0 {
		t.Fatalf("a local hit made %d shared-cache GET(s); it must make none", gets)
	}
}

// A miss on both tiers is still a miss, and it must be the ordinary
// entryNotFoundError callers already branch on.
func TestSharedCache_MissEverywhereReportsANormalMiss(t *testing.T) {
	_, srv := newFakeCacheServer(t)
	configureShared(t, srv)

	c := openShared(t, t.TempDir())
	defer c.Close()

	_, err := c.Get(testActionID("never-stored"))
	if err == nil {
		t.Fatal("Get of an absent key must fail")
	}
	if _, ok := err.(*entryNotFoundError); !ok {
		t.Fatalf("Get returned %T (%v), want *entryNotFoundError", err, err)
	}
}

// With nothing configured there is no shared tier at all, so a developer's
// machine keeps the plain on-disk cache and never dials anything.
func TestSharedCache_UnconfiguredIsNoTier(t *testing.T) {
	t.Setenv(cacheclient.ConfigEnv, "")
	if Shared() {
		t.Fatal("Shared reported a tier with no configuration")
	}
	disk, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c := newSharedCache(disk); c != nil {
		t.Fatalf("newSharedCache built a %T with no configuration", c)
	}
}

// A configured shared tier must be chosen over a cache program, so an org
// build talks to the cache in process and never forks one. The program named
// here does not exist: starting it would kill the build, which is what makes
// this assert the choice rather than the outcome.
func TestChooseCache_SharedTierBeatsACacheProgram(t *testing.T) {
	_, srv := newFakeCacheServer(t)
	configureShared(t, srv)

	disk, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := chooseCache(disk, "no-such-cache-program-should-ever-run")
	if _, ok := c.(*SharedCache); !ok {
		t.Fatalf("chooseCache returned %T, want *SharedCache", c)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// With no shared tier configured, a cache program is still honored: this fork
// does not take GOCACHEPROG away from a cache it knows nothing about.
func TestChooseCache_NoSharedTierKeepsTheCacheProgram(t *testing.T) {
	t.Setenv(cacheclient.ConfigEnv, "")

	disk, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c := chooseCache(disk, ""); c != Cache(disk) {
		t.Fatalf("chooseCache with no tier and no program returned %T, want the disk cache", c)
	}
}

// Outside CI, an unconfigured shared cache is a developer's ordinary machine,
// never a build failure.
func TestValidateCIShared_NotCIPasses(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv(cacheclient.ConfigEnv, "")
	if err := validateCIShared(); err != nil {
		t.Fatalf("validateCIShared outside CI: %v", err)
	}
}

// A CI run with the shared tier configured passes, whatever else CI carries.
func TestValidateCIShared_CIWithSharedConfiguredPasses(t *testing.T) {
	t.Setenv("CI", "true")
	_, srv := newFakeCacheServer(t)
	configureShared(t, srv)
	if err := validateCIShared(); err != nil {
		t.Fatalf("validateCIShared with a configured shared tier: %v", err)
	}
}

// A CI run with no shared tier configured must fail outright: no env var
// downgrades this to a warning.
func TestValidateCIShared_CIWithoutSharedFails(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv(cacheclient.ConfigEnv, "")
	err := validateCIShared()
	if err == nil {
		t.Fatal("validateCIShared in CI with no shared cache must fail")
	}
	if !strings.Contains(err.Error(), "GO_BUILDCACHE_CONFIG") {
		t.Fatalf("error %q must name the missing variable", err)
	}
}

// captureStderr swaps os.Stderr for a pipe across fn and returns what was
// written. The swap happens before fn runs because the client captures
// os.Stderr as a writer when it builds its backend, not per message.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stderr = old
	w.Close()
	out := <-done
	r.Close()
	return out
}

// A shared tier in trouble must not write to the go command's stderr. The
// fake server refuses the index endpoint, which is exactly what an unwell
// server does in production, and the client says so on every build. That
// output is not a build diagnostic -- a cache tier cannot change what a
// build produces -- and cmd/internal/testdir asserts a go run prints
// nothing, so a talkative cache turns an unrelated service's health into
// red tests across the tree.
func TestSharedCache_DiagnosticsStayOffTheBuildsOutput(t *testing.T) {
	_, srv := newFakeCacheServer(t)
	configureShared(t, srv)
	t.Setenv(CacheDebugEnv, "")

	out := captureStderr(t, func() {
		c := openShared(t, t.TempDir())
		c.(*SharedCache).Get(testActionID("quiet"))
	})
	if out != "" {
		t.Fatalf("shared tier wrote to the build's stderr:\n%s", out)
	}
}

// The client's HTTP error summaries do not go through the Logger: the
// backend captures a writer once, when it is built, and that writer was
// os.Stderr. So a cache server refusing writes put lines on the go
// command's stderr no matter what this package installed. A build here must
// not depend on that server's health, and it must not depend on a fix
// landing in that server's repo either -- it is built with this toolchain,
// so this tree cannot wait on it.
//
// This also guards the assumption newWebBackend rests on. If the client ever
// captures the writer later than construction, this goes red.
func TestSharedCache_HTTPErrorsStayOffTheBuildsOutput(t *testing.T) {
	f, srv := newFakeCacheServer(t)
	f.failPuts = true
	configureShared(t, srv)
	t.Setenv(CacheDebugEnv, "")

	out := captureStderr(t, func() {
		c := openShared(t, t.TempDir())
		if err := PutBytes(c, testActionID("refused"), []byte("body")); err != nil {
			t.Logf("put failed, which is the point: %v", err)
		}
		c.(*SharedCache).Close()
	})
	if out != "" {
		t.Fatalf("refused uploads reached the build's stderr:\n%s", out)
	}
}

// The diagnostics are not deleted, only held back. Anyone debugging the cache
// sets GOCACHEDEBUG and gets the same messages back during the window.
func TestSharedCache_CacheDebugRestoresDiagnostics(t *testing.T) {
	_, srv := newFakeCacheServer(t)
	configureShared(t, srv)
	t.Setenv(CacheDebugEnv, "1")

	out := captureStderr(t, func() {
		c := openShared(t, t.TempDir())
		c.(*SharedCache).Get(testActionID("loud"))
	})
	if !strings.Contains(out, "cacheprog:") {
		t.Fatalf("GOCACHEDEBUG=1 must report the tier's trouble, got:\n%s", out)
	}
}

// The window ends by itself. Past the deadline a failing tier reports again
// with nobody editing anything, which is what makes this an outage window
// rather than a permanently muted warning.
//
// This test also fails once the real deadline passes and the window was
// never removed, so extending it is a deliberate edit here and not a drift.
func TestSharedCache_QuietWindowExpires(t *testing.T) {
	if !cacheQuietUntil.After(time.Now()) {
		t.Fatalf("the quiet window closed at %s: delete it and the gates that read it, "+
			"or say here why it moves", cacheQuietUntil.Format(time.RFC3339))
	}

	saved := cacheQuietUntil
	t.Cleanup(func() { cacheQuietUntil = saved })
	cacheQuietUntil = time.Now().Add(-time.Second)

	f, srv := newFakeCacheServer(t)
	f.failPuts = true
	configureShared(t, srv)
	t.Setenv(CacheDebugEnv, "")

	out := captureStderr(t, func() {
		c := openShared(t, t.TempDir())
		PutBytes(c, testActionID("expired"), []byte("body"))
		c.(*SharedCache).Close()
	})
	if !strings.Contains(out, "cacheprog:") {
		t.Fatalf("past the deadline a failing tier must report again, got:\n%s", out)
	}
}

// readOutputFile reads what OutputFile names, which is what the compiler does
// with the path a cache hit hands it.
func readOutputFile(c Cache, id OutputID) ([]byte, error) {
	name := c.OutputFile(id)
	if name == "" {
		return nil, fmt.Errorf("OutputFile(%x) is empty", id)
	}
	return os.ReadFile(name)
}
