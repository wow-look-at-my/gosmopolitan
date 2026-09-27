// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package orgmod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

const (
	alphaPath = "github.com/wow-look-at-my/alpha"
	headA     = "v0.0.0-20260102030405-aaaaaaaaaaaa"
	headB     = "v0.0.0-20260103040506-bbbbbbbbbbbb"
)

var testRun = Run{Repository: "wow-look-at-my/consumer", ID: "4242", Attempt: "1"}

// memStore is a RunLockStore in memory. With blind set, Lookup finds nothing,
// so every caller races to Claim.
type memStore struct {
	mu      sync.Mutex
	locks   map[RunLockKey]string
	blind   bool
	fail    error
	lookups int
	claims  int
}

func newMemStore() *memStore { return &memStore{locks: map[RunLockKey]string{}} }

func (s *memStore) String() string { return "mem://test" }

func (s *memStore) Lookup(ctx context.Context, key RunLockKey) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookups++
	if s.fail != nil {
		return "", false, s.fail
	}
	if s.blind {
		return "", false, nil
	}
	v, ok := s.locks[key]
	return v, ok, nil
}

func (s *memStore) Claim(ctx context.Context, key RunLockKey, version string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if s.fail != nil {
		return "", s.fail
	}
	if v, ok := s.locks[key]; ok {
		return v, nil
	}
	s.locks[key] = version
	return version, nil
}

func key(branch string) RunLockKey {
	return RunLockKey{Run: testRun, Module: alphaPath, Branch: branch}
}

func resolveTo(version string, calls *int) func() (string, error) {
	return func() (string, error) {
		*calls++
		return version, nil
	}
}

func TestLockedVersionFirstWriterWins(t *testing.T) {
	store := newMemStore()
	var calls int
	got, err := LockedVersion(context.Background(), store, key("main"), resolveTo(headA, &calls))
	if err != nil || got != headA {
		t.Fatalf("first LockedVersion = %q, %v; want %q", got, err, headA)
	}
	// The head moves, and the next process of the run reads the lock.
	got, err = LockedVersion(context.Background(), store, key("main"), resolveTo(headB, &calls))
	if err != nil || got != headA {
		t.Fatalf("second LockedVersion = %q, %v; want the locked %q", got, err, headA)
	}
	if calls != 1 {
		t.Errorf("resolve ran %d times, want once: a locked module resolves no head", calls)
	}
	// Another branch and another attempt are other locks.
	got, err = LockedVersion(context.Background(), store, key("v1"), resolveTo(headB, &calls))
	if err != nil || got != headB {
		t.Errorf("LockedVersion on another branch = %q, %v; want %q", got, err, headB)
	}
	retry := key("main")
	retry.Attempt = "2"
	got, err = LockedVersion(context.Background(), store, retry, resolveTo(headB, &calls))
	if err != nil || got != headB {
		t.Errorf("LockedVersion in another attempt = %q, %v; want %q", got, err, headB)
	}
}

func TestLockedVersionSecondClaimGetsFirstValue(t *testing.T) {
	store := newMemStore()
	store.blind = true
	var calls int
	if got, _ := LockedVersion(context.Background(), store, key("main"), resolveTo(headA, &calls)); got != headA {
		t.Fatalf("first claim = %q, want %q", got, headA)
	}
	got, err := LockedVersion(context.Background(), store, key("main"), resolveTo(headB, &calls))
	if err != nil || got != headA {
		t.Errorf("second claim = %q, %v; want the first claim's %q", got, err, headA)
	}
}

func TestLockedVersionRacingWritersConverge(t *testing.T) {
	store := newMemStore()
	store.blind = true
	const writers = 32
	results := make([]string, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			head := fmt.Sprintf("v0.0.0-20260102030405-%012d", i)
			got, err := LockedVersion(context.Background(), store, key("main"), func() (string, error) { return head, nil })
			if err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
			results[i] = got
		}()
	}
	wg.Wait()
	winner := store.locks[key("main")]
	for i, got := range results {
		if got != winner {
			t.Errorf("writer %d got %q, want the winner %q", i, got, winner)
		}
	}
	if store.claims != writers {
		t.Errorf("store saw %d claims, want %d", store.claims, writers)
	}
}

func TestLockedVersionStoreErrorFails(t *testing.T) {
	store := newMemStore()
	store.fail = errors.New("connection refused")
	var calls int
	_, err := LockedVersion(context.Background(), store, key("main"), resolveTo(headA, &calls))
	if err == nil {
		t.Fatal("LockedVersion with a failing store succeeded")
	}
	for _, part := range []string{alphaPath + "@main", "mem://test", "connection refused"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not name %q", err, part)
		}
	}
	if calls != 0 {
		t.Errorf("resolve ran with a failing store; the head must never stand in for the lock")
	}

	// A claim that fails after a lookup that answered is the same failure.
	failing := &claimFails{memStore: newMemStore()}
	_, err = LockedVersion(context.Background(), failing, key("main"), resolveTo(headA, &calls))
	if err == nil || !strings.Contains(err.Error(), "mem://test") || !strings.Contains(err.Error(), alphaPath) {
		t.Errorf("LockedVersion with a failing claim = %v; want an error that names the store and the module", err)
	}
}

type claimFails struct{ *memStore }

func (s *claimFails) Claim(ctx context.Context, key RunLockKey, version string) (string, error) {
	return "", errors.New("503 Service Unavailable")
}

func TestVersionOutsideCINeverTouchesStore(t *testing.T) {
	open := func() (RunLockStore, Run, error) {
		t.Fatal("a build outside CI opened the run lock store")
		return nil, Run{}, nil
	}
	var calls int
	got, err := Version(context.Background(), false, open, alphaPath, "main", resolveTo(headB, &calls))
	if err != nil || got != headB || calls != 1 {
		t.Errorf("Version outside CI = %q, %v after %d resolves; want the head %q", got, err, calls, headB)
	}
}

func TestVersionInCIUsesLock(t *testing.T) {
	store := newMemStore()
	store.locks[key("main")] = headA
	open := func() (RunLockStore, Run, error) { return store, testRun, nil }
	var calls int
	got, err := Version(context.Background(), true, open, alphaPath, "main", resolveTo(headB, &calls))
	if err != nil || got != headA {
		t.Errorf("Version in CI = %q, %v; want the locked %q", got, err, headA)
	}

	open = func() (RunLockStore, Run, error) { return nil, Run{}, errors.New("GITHUB_RUN_ID is not set") }
	_, err = Version(context.Background(), true, open, alphaPath, "main", resolveTo(headB, &calls))
	if err == nil || !strings.Contains(err.Error(), alphaPath+"@main") {
		t.Errorf("Version with no store = %v; want an error that names the module", err)
	}
}

func TestOpenRunLock(t *testing.T) {
	ci := map[string]string{
		"GITHUB_REPOSITORY":  testRun.Repository,
		"GITHUB_RUN_ID":      testRun.ID,
		"GITHUB_RUN_ATTEMPT": testRun.Attempt,
	}
	with := func(extra map[string]string) map[string]string {
		env := map[string]string{}
		for k, v := range ci {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		return env
	}

	for _, name := range []string{"GITHUB_REPOSITORY", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT"} {
		_, err := openRunLock(envOf(with(map[string]string{name: ""})))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("openRunLock without %s = %v; want an error that names it", name, err)
		}
	}

	// The default store is buildhost, which needs the job's OIDC token.
	_, err := openRunLock(envOf(ci))
	if err == nil || !strings.Contains(err.Error(), DefaultRunLockStore) || !strings.Contains(err.Error(), "id-token: write") {
		t.Errorf("openRunLock with no OIDC token = %v; want an error that names the store and the permission", err)
	}
	lock, err := openRunLock(envOf(with(map[string]string{
		"ACTIONS_ID_TOKEN_REQUEST_URL":   "https://token.example/?x=1",
		"ACTIONS_ID_TOKEN_REQUEST_TOKEN": "bearer",
	})))
	if err != nil || lock.store.String() != DefaultRunLockStore || lock.run != testRun {
		t.Errorf("openRunLock = %v, %v; want %s for run %v", lock, err, DefaultRunLockStore, testRun)
	}

	dir := t.TempDir()
	lock, err = openRunLock(envOf(with(map[string]string{RunLockEnv: "file://" + dir})))
	if err != nil {
		t.Fatal(err)
	}
	if fs, ok := lock.store.(fileStore); !ok || fs.dir != dir {
		t.Errorf("openRunLock with a file URL = %#v, want a file store in %s", lock.store, dir)
	}

	_, err = openRunLock(envOf(with(map[string]string{RunLockEnv: "ftp://example.com/locks"})))
	if err == nil || !strings.Contains(err.Error(), "ftp://example.com/locks") {
		t.Errorf("openRunLock with an ftp URL = %v; want an error that names it", err)
	}
}

func TestFileStoreClaimsRace(t *testing.T) {
	store := fileStore{raw: "file://test", dir: t.TempDir() + "/locks"}
	if _, found, err := store.Lookup(context.Background(), key("main")); found || err != nil {
		t.Fatalf("Lookup in an empty store = %v, %v; want nothing", found, err)
	}
	const writers = 16
	results := make([]string, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := store.Claim(context.Background(), key("main"), fmt.Sprintf("v0.0.0-20260102030405-%012d", i))
			if err != nil {
				t.Errorf("claim %d: %v", i, err)
			}
			results[i] = got
		}()
	}
	wg.Wait()
	winner, found, err := store.Lookup(context.Background(), key("main"))
	if !found || err != nil || winner == "" {
		t.Fatalf("Lookup after the claims = %q, %v, %v", winner, found, err)
	}
	for i, got := range results {
		if got != winner {
			t.Errorf("claim %d returned %q, want the winner %q", i, got, winner)
		}
	}
}

func TestFileStoreFailsLoudly(t *testing.T) {
	// A directory path that runs through a regular file cannot hold locks.
	blocker := t.TempDir() + "/blocker"
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o666); err != nil {
		t.Fatal(err)
	}
	store := fileStore{raw: "file://" + blocker, dir: blocker + "/locks"}
	var calls int
	_, err := LockedVersion(context.Background(), store, key("main"), resolveTo(headA, &calls))
	if err == nil || !strings.Contains(err.Error(), "file://"+blocker) {
		t.Errorf("LockedVersion on an unusable directory = %v; want an error that names the store", err)
	}
}

func TestHTTPStore(t *testing.T) {
	locks := map[string]string{}
	var mu sync.Mutex
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if r.Header.Get("Authorization") != "Bearer request-token" {
				http.Error(w, "bad request token", http.StatusUnauthorized)
				return
			}
			if got := r.URL.Query().Get("audience"); got != srv.URL {
				http.Error(w, "audience "+got, http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"value": "oidc-jwt"})
			return
		case r.Header.Get("Authorization") != "Bearer oidc-jwt":
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		case r.URL.Path != "/api/v1/run-locks":
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var body runLockBody
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			q := r.URL.Query()
			body = runLockBody{Repository: q.Get("repository"), RunID: q.Get("run_id"), RunAttempt: q.Get("run_attempt"), Name: q.Get("name")}
		}
		if body.Repository != testRun.Repository || body.RunID != testRun.ID || body.RunAttempt != testRun.Attempt {
			http.Error(w, `{"error":"run does not match the token"}`, http.StatusForbidden)
			return
		}
		id := body.Name
		answer := runLockBody{}
		if r.Method == http.MethodPost {
			if _, ok := locks[id]; !ok {
				locks[id] = body.Value
				answer.Created = true
			}
			answer.Value, answer.Found = locks[id], true
		} else {
			answer.Value, answer.Found = locks[id]
		}
		json.NewEncoder(w).Encode(answer)
	}))
	defer srv.Close()

	env := map[string]string{
		"GITHUB_REPOSITORY":              testRun.Repository,
		"GITHUB_RUN_ID":                  testRun.ID,
		"GITHUB_RUN_ATTEMPT":             testRun.Attempt,
		RunLockEnv:                       srv.URL,
		"ACTIONS_ID_TOKEN_REQUEST_URL":   srv.URL + "/token?api-version=2.0",
		"ACTIONS_ID_TOKEN_REQUEST_TOKEN": "request-token",
	}
	lock, err := openRunLock(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	got, err := LockedVersion(context.Background(), lock.store, RunLockKey{lock.run, alphaPath, "main"}, resolveTo(headA, &calls))
	if err != nil || got != headA {
		t.Fatalf("first LockedVersion = %q, %v; want %q", got, err, headA)
	}
	got, err = LockedVersion(context.Background(), lock.store, RunLockKey{lock.run, alphaPath, "main"}, resolveTo(headB, &calls))
	if err != nil || got != headA || calls != 1 {
		t.Errorf("second LockedVersion = %q, %v after %d resolves; want the locked %q", got, err, calls, headA)
	}
	if got, _ := lock.store.Claim(context.Background(), RunLockKey{lock.run, alphaPath, "main"}, headB); got != headA {
		t.Errorf("a late claim = %q, want the first claim's %q", got, headA)
	}

	// A store that refuses the run fails the resolution, naming the store.
	other := lock.run
	other.ID = "1"
	_, err = LockedVersion(context.Background(), lock.store, RunLockKey{other, alphaPath, "main"}, resolveTo(headB, &calls))
	if err == nil || !strings.Contains(err.Error(), srv.URL) || !strings.Contains(err.Error(), "403") {
		t.Errorf("LockedVersion refused by the store = %v; want an error that names the store and the refusal", err)
	}

	// A token the job cannot get fails the same way.
	env["ACTIONS_ID_TOKEN_REQUEST_TOKEN"] = "wrong"
	lock, err = openRunLock(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	_, err = LockedVersion(context.Background(), lock.store, RunLockKey{lock.run, alphaPath, "v1"}, resolveTo(headB, &calls))
	if err == nil || !strings.Contains(err.Error(), "OIDC token") || !strings.Contains(err.Error(), srv.URL) {
		t.Errorf("LockedVersion with no OIDC token = %v; want an error that names the token and the store", err)
	}
}
