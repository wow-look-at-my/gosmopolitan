// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package orgmod

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// A CI run locks the version of each org module it builds.

// RunLockEnv names the environment variable that names the store as a URL. A file URL names a directory.
const RunLockEnv = "GOSMOPOLITAN_RUN_LOCK_STORE"

// RunEnv names the run as owner/repo/run-id/attempt.
const RunEnv = "GOSMOPOLITAN_RUN"

// DefaultRunLockStore is the buildhost server that holds the run locks.
const DefaultRunLockStore = "https://pazer.build"

// A Run is one attempt of one workflow run of one repository.
type Run struct {
	Repository string // GITHUB_REPOSITORY, as owner/repo
	ID         string // GITHUB_RUN_ID
	Attempt    string // GITHUB_RUN_ATTEMPT
}

// A RunLockKey names one lock: an org module on a branch, in one run.
type RunLockKey struct {
	Run
	Module string
	Branch string
}

// Name is the lock's name inside its run.
func (k RunLockKey) Name() string { return k.Module + "@" + k.Branch }

// A RunLockStore holds the locks of runs.
type RunLockStore interface {
	// Lookup returns the version recorded under key, and whether one is.
	Lookup(ctx context.Context, key RunLockKey) (version string, found bool, err error)
	// Claim records version under key unless the store holds a version there already.
	Claim(ctx context.Context, key RunLockKey, version string) (string, error)
	// String names the store in an error.
	String() string
}

// Version returns the version of the org module at path on branch that this
// process builds. Outside a CI build that is the head resolve returns, and open
// is never called. A CI build takes the version its run locked, and fails when
// the store fails.
func Version(ctx context.Context, ci bool, open func() (RunLockStore, Run, error), path, branch string, resolve func() (string, error)) (string, error) {
	if !ci {
		version, err := resolve()
		if err == nil {
			logVersion(path, branch, version, "the branch head, this build resolving it for itself")
		}
		return version, err
	}
	store, run, err := open()
	if err != nil {
		return "", fmt.Errorf("%s@%s: %w", path, branch, err)
	}
	return LockedVersion(ctx, store, RunLockKey{Run: run, Module: path, Branch: branch}, resolve)
}

// LockedVersion returns the version the store records for key. When it records
// none, LockedVersion claims the head that resolve returns and returns the
// version the claim leaves in the store, which a racing claim can have set.
func LockedVersion(ctx context.Context, store RunLockStore, key RunLockKey, resolve func() (string, error)) (string, error) {
	fail := func(err error) error {
		return fmt.Errorf("%s: run lock store %s: %w", key.Name(), store, err)
	}
	// A lock never changes inside its run, so the copy on this machine is the store's answer.
	kept, keeping := keptLocks(store)
	if keeping {
		if version, found, err := kept.Lookup(ctx, key); err == nil && found && version != "" {
			return version, nil
		}
	}
	version, found, err := store.Lookup(ctx, key)
	if err != nil {
		return "", fail(err)
	}
	origin := "the version this run locked earlier"
	if !found {
		head, err := resolve()
		if err != nil {
			return "", err
		}
		version, err = store.Claim(ctx, key, head)
		if err != nil {
			return "", fail(err)
		}
		origin = "the branch head, locked here for the rest of this run"
		if version != head {
			origin = "the branch head a racing command of this run locked first"
		}
	}
	if version == "" {
		return "", fail(errors.New("the store holds an empty version"))
	}
	if keeping {
		if _, err := kept.Claim(ctx, key, version); err != nil {
			fmt.Fprintf(logOutput, "go: %s: cannot keep the run lock in %s, so the next go command asks %s again: %v\n", key.Name(), kept.dir, store, err)
		}
	}
	logVersion(key.Module, key.Branch, version, origin)
	return version, nil
}

// logVersion names the version an org module built at, and which of the ways
// chose it.
func logVersion(path, branch, version, origin string) {
	fmt.Fprintf(logOutput, "go: %s@%s: building %s -- %s\n", path, branch, version, origin)
}

var logOutput io.Writer = os.Stderr

// keptLocks returns this machine's copy of the locks store holds, under
// StateDir. Each store has its own copy, so a store that fails still fails the
// build. The key names the run and the attempt, so a copy never outlives them.
func keptLocks(store RunLockStore) (fileStore, bool) {
	root := StateDir()
	if root == "" {
		return fileStore{}, false
	}
	sum := sha256.Sum256([]byte(store.String()))
	dir := filepath.Join(root, "run-locks", hex.EncodeToString(sum[:12]))
	return fileStore{raw: dir, dir: dir}, true
}

// OpenRunLock returns the store and the run of this process. It reads the
// environment once.
var OpenRunLock = sync.OnceValues(func() (runLock, error) {
	return openRunLock(os.Getenv)
})

// CurrentRunLock is OpenRunLock in the shape Version takes.
func CurrentRunLock() (RunLockStore, Run, error) {
	lock, err := OpenRunLock()
	return lock.store, lock.run, err
}

type runLock struct {
	store RunLockStore
	run   Run
}

// openRunLock reads the run and the store from getenv.
func openRunLock(getenv func(string) string) (runLock, error) {
	run := Run{
		Repository: getenv("GITHUB_REPOSITORY"),
		ID:         getenv("GITHUB_RUN_ID"),
		Attempt:    getenv("GITHUB_RUN_ATTEMPT"),
	}
	if run.ID == "" || run.Attempt == "" {
		if raw := getenv(RunEnv); raw != "" {
			parts := strings.Split(raw, "/")
			if len(parts) != 4 || slices.Contains(parts, "") {
				return runLock{}, fmt.Errorf("%s=%q: want owner/repo/run-id/attempt", RunEnv, raw)
			}
			run = Run{Repository: parts[0] + "/" + parts[1], ID: parts[2], Attempt: parts[3]}
		}
	}
	for _, v := range []struct{ name, val string }{
		{"GITHUB_REPOSITORY", run.Repository},
		{"GITHUB_RUN_ID", run.ID},
		{"GITHUB_RUN_ATTEMPT", run.Attempt},
	} {
		if v.val == "" {
			return runLock{}, fmt.Errorf("a CI build locks org modules per run, and neither %s nor %s is set", v.name, RunEnv)
		}
	}
	raw := getenv(RunLockEnv)
	if raw == "" {
		raw = DefaultRunLockStore
	}
	u, err := url.Parse(raw)
	if err != nil {
		return runLock{}, fmt.Errorf("run lock store %s: %v", raw, err)
	}
	switch u.Scheme {
	case "file":
		return runLock{fileStore{raw: raw, dir: fileURLPath(u)}, run}, nil
	case "https", "http":
		store, err := newHTTPStore(u, getenv)
		if err != nil {
			return runLock{}, fmt.Errorf("run lock store %s: %w", raw, err)
		}
		return runLock{store, run}, nil
	}
	return runLock{}, fmt.Errorf("run lock store %s: %s names neither a file URL nor an HTTP one", raw, RunLockEnv)
}

// fileURLPath returns the local path a file URL names.
func fileURLPath(u *url.URL) string {
	path := u.Path
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}

// A fileStore keeps each lock in a file of one directory. Every job that
// shares the directory shares the locks.
type fileStore struct {
	raw string
	dir string
}

func (s fileStore) String() string { return s.raw }

// file returns the path of the file that holds key.
func (s fileStore) file(key RunLockKey) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{key.Repository, key.ID, key.Attempt, key.Module, key.Branch}, "\x00")))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:]))
}

func (s fileStore) Lookup(ctx context.Context, key RunLockKey) (string, bool, error) {
	data, err := os.ReadFile(s.file(key))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// Claim writes the version to a temporary file and links it into place. A link
// fails when the name exists, so a reader never sees a half-written lock and a
// second claim never replaces the first.
func (s fileStore) Claim(ctx context.Context, key RunLockKey, version string) (string, error) {
	if err := os.MkdirAll(s.dir, 0o777); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(s.dir, ".claim-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(version)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	name := s.file(key)
	if err := os.Link(tmp.Name(), name); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
