// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package orgmod

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
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

// RunLocked reports whether this build locks the org module heads of a run.
var RunLocked = sync.OnceValue(func() bool {
	return CIBuild() || os.Getenv(RunEnv) != ""
})

// Version returns the version of the org module at path on branch that this
// process builds. A build that locks no run takes the head resolve returns,
// and open is never called. A locked build takes the version its run locked,
// and fails when the store fails.
func Version(ctx context.Context, locked bool, open func() (RunLockStore, Run, error), path, branch string, resolve func() (string, error)) (string, error) {
	if !locked {
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

// LockedVersion returns the version the store records for key. LockedVersion
// claims the head that resolve returns and returns the version the claim
// leaves in the store, which a racing claim can have set. Do this when it
// records none.
func LockedVersion(ctx context.Context, store RunLockStore, key RunLockKey, resolve func() (string, error)) (string, error) {
	fail := func(err error) error {
		return fmt.Errorf("%s: run lock store %s: %w", key.Name(), store, err)
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
	logVersion(key.Module, key.Branch, version, origin)
	return version, nil
}

// logVersion names the version an org module built at, and which of the ways
// chose it.
func logVersion(path, branch, version, origin string) {
	fmt.Fprintf(os.Stderr, "go: %s@%s: building %s -- %s\n", path, branch, version, origin)
}

// OpenRunLock returns the store and the run of this process. It reads the
// environment once.
var OpenRunLock = sync.OnceValues(func() (runLock, error) {
	return openRunLock(CIBuild(), os.Getenv)
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

// openRunLock reads the run and the store from getenv. ci is whether this build
// is a CI job, which decides the store a run defaults to.
func openRunLock(ci bool, getenv func(string) string) (runLock, error) {
	run, err := runFromEnv(getenv)
	if err != nil {
		return runLock{}, err
	}
	raw := getenv(RunLockEnv)
	if raw == "" {
		if !ci {
			store := localStore(run)
			return runLock{store, run}, nil
		}
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

// runFromEnv reads the run this build belongs to. A CI job names it in the
// GitHub Actions variables; a caller that runs several go commands in one
// local build names it in RunEnv.
func runFromEnv(getenv func(string) string) (Run, error) {
	run := Run{
		Repository: getenv("GITHUB_REPOSITORY"),
		ID:         getenv("GITHUB_RUN_ID"),
		Attempt:    getenv("GITHUB_RUN_ATTEMPT"),
	}
	if run.ID == "" || run.Attempt == "" {
		if raw := getenv(RunEnv); raw != "" {
			parts := strings.Split(raw, "/")
			if len(parts) != 4 || slices.Contains(parts, "") {
				return Run{}, fmt.Errorf("%s=%q: want owner/repo/run-id/attempt", RunEnv, raw)
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
			return Run{}, fmt.Errorf("a CI build locks org modules per run, and neither %s nor %s is set", v.name, RunEnv)
		}
	}
	return run, nil
}

// localStore is the store a local run locks its heads in: a directory of its
// own under the user cache.
func localStore(run Run) fileStore {
	dir := filepath.Join(localRunLockRoot(), run.slug())
	pruneLocalRunLocks()
	return fileStore{raw: "file://" + filepath.ToSlash(dir), dir: dir}
}

// localRunLockRoot is the directory that holds every local run's locks.
func localRunLockRoot() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "gosmopolitan", "run-locks")
}

// localRunLockMaxAge is how long a local run's locks are kept after its last command wrote one.
const localRunLockMaxAge = 7 * 24 * time.Hour

// localRunLockPrune runs the sweep once per process.
var localRunLockPrune sync.Once

// pruneLocalRunLocks removes the run directories no build can still be using.
// The sweep is best effort: a directory that cannot be read or removed is left
// for the next build.
func pruneLocalRunLocks() {
	localRunLockPrune.Do(func() {
		root := localRunLockRoot()
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		cutoff := time.Now().Add(-localRunLockMaxAge)
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	})
}

// slug names a run in a directory name, so runs never share a store.
func (r Run) slug() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{r.Repository, r.ID, r.Attempt}, "\x00")))
	return hex.EncodeToString(sum[:16])
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
