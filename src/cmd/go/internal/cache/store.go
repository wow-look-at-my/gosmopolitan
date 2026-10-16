// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cmd/go/internal/base"
	goCfg "cmd/go/internal/cfg"
	"cmd/internal/objabi"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

// openCache opens the directory, puts the store under it when one is
// configured, and serves the result to the go commands this starts.
func openCache(dir string) (Cache, error) {
	store, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	cache, err := cacheclient.OpenCache(dir, store)
	if err != nil {
		return nil, err
	}
	if store != nil {
		publishStore(store)
	}
	// A test binary and a `go run` program are started with the environment this command was started with, rather than with this process's own.
	goCfg.OrigEnv = append(goCfg.OrigEnv[:len(goCfg.OrigEnv):len(goCfg.OrigEnv)], cacheclient.BrokerEnviron()...)
	// A go command that never runs a build still opens the cache, and a failing
	// one exits through base.Exit without returning.
	base.AtExit(func() {
		cacheclient.CloseStore()
		cacheclient.Exit()
	})
	return cache, nil
}

// RunUploadSpool runs the upload spool this process was started for, when the
// environment names one. It reports the exit status and whether it ran. Such
// a process opens no cache and serves no build.
func RunUploadSpool() (code int, ran bool) {
	path := os.Getenv(cacheclient.UploadSpoolEnv)
	if path == "" {
		return 0, false
	}
	cacheclient.SetLogger(goLogger{})
	if path == cacheclient.UploadSpoolAll {
		// A CI job's last step: the runner ends a detached uploader with the job.
		dir, _, err := DefaultDir()
		if err == nil {
			err = cacheclient.DrainSpools(dir)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "go: %v\n", err)
			return 1, true
		}
		return 0, true
	}
	if err := cacheclient.RunUploadSpool(path); err != nil {
		fmt.Fprintf(os.Stderr, "go: %v\n", err)
		return 1, true
	}
	return 0, true
}

// The store is configured by the environment. What the build IS comes from the
// build itself: the target it produces and the module it builds. The store has
// no other way to learn either. Its log would otherwise say how many objects
// moved and nothing about whose build moved them.

// openStore builds the shared tier this command reaches, or nothing when the
// environment configures none. A CI run with none configured is an error: a
// build that silently stops sharing is how a cache regression hides.
func openStore(dir string) (*cacheclient.WebBackend, error) {
	cfg := cacheclient.ConfigFromEnv()
	if cfg.Bucket == "" {
		if os.Getenv("CI") != "" {
			return nil, fmt.Errorf("CI build cache not configured: GO_BUILDCACHE_CONFIG is not set")
		}
		return nil, nil
	}
	cfg.Target = goCfg.Goos + "/" + goCfg.Goarch
	// The client keys its build manifest on this. A publish stamp alone changes no cache key, so it is left out.
	cfg.Version = stripPublishStamp(runtime.Version()) + " compile=" + objabi.ToolBuildID("compile")
	cfg.Module = mainModulePath()
	cfg.Command = commandDigest()
	cfg.IndexDir = indexDir(dir)
	cacheclient.SetLogger(goLogger{})
	// RunAs serves the spool, so Close need not wait for the uploads.
	cacheclient.HandOffUploads()
	store, err := cacheclient.NewWebBackend(cfg)
	if err != nil {
		// A store that cannot be reached is a slower build, not a broken one.
		fmt.Fprintf(os.Stderr, "go: shared build cache disabled: %v\n", err)
		return nil, nil
	}
	return store, nil
}

// indexDir returns the directory for the key index's disk copy.
func indexDir(dir string) string {
	userCache, err := os.UserCacheDir()
	if err != nil || userCache == "" {
		return dir
	}
	shared := filepath.Join(userCache, "go-build-index")
	if err := os.MkdirAll(shared, 0o777); err != nil {
		return dir
	}
	return shared
}

// publishStore makes store the SetSharedModule names the module to. The
// module loader can run while the cache opens.
func publishStore(store *cacheclient.WebBackend) {
	liveStore.Store(store)
	if path := mainModulePath(); path != "" {
		store.SetModule(path)
	}
}

// commandDigest names what this go command builds: its arguments and the
// directory they resolve in. The store keeps one build manifest per command.
func commandDigest() string {
	wd, _ := os.Getwd()
	sum := sha256.Sum256([]byte(strings.Join(append([]string{wd}, os.Args[1:]...), "\x00")))
	return hex.EncodeToString(sum[:16])
}

// liveStore is this command's store, once it has one.
var liveStore atomic.Pointer[cacheclient.WebBackend]

// sharedModule is the main module, which the module loader learns after this command has already opened its cache.
var sharedModule atomic.Pointer[string]

// SetSharedModule records which module's build is running, for the store's
// request headers.
func SetSharedModule(path string) {
	if path == "" {
		return
	}
	sharedModule.Store(&path)
	if store := liveStore.Load(); store != nil {
		store.SetModule(path)
	}
}

// mainModulePath is the module this build builds, or "".
func mainModulePath() string {
	if path := sharedModule.Load(); path != nil {
		return *path
	}
	return ""
}

// goLogger takes the cache's diagnostics.
type goLogger struct{}

// CacheLogEnv names a file that takes the cache's notices in place of stderr.
const CacheLogEnv = "GOCACHELOG"

// PlumbingEnv lists the variables that carry one build's cache plumbing to the processes it starts.
var PlumbingEnv = []string{CacheLogEnv, cacheclient.BrokerEnv}

var (
	cacheLogOnce sync.Once
	cacheLogFile *os.File // nil: the notices go to stderr
)

// cacheNotice writes one notice where CacheLogEnv says. A file that cannot be
// opened is reported once, and the notices fall back to stderr.
func cacheNotice(format string, args ...any) {
	cacheLogOnce.Do(func() {
		path := os.Getenv(CacheLogEnv)
		if path == "" {
			return
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go: %s: %v; the cache notices go to stderr\n", CacheLogEnv, err)
			return
		}
		cacheLogFile = file
	})
	if cacheLogFile == nil {
		fmt.Fprintf(os.Stderr, "go: "+format+"\n", args...)
		return
	}
	stamp := fmt.Sprintf("%s [%d] ", time.Now().Format(time.TimeOnly), os.Getpid())
	fmt.Fprintf(cacheLogFile, stamp+format+"\n", args...)
}

// MissNotices reports whether a test result the cache did not have is
// explained.
func MissNotices() bool {
	return os.Getenv(CacheLogEnv) != ""
}

// MissNotice writes one line of such an explanation to the notice file.
func MissNotice(format string, args ...any) {
	if !MissNotices() {
		return
	}
	cacheNotice(format, args...)
}

func (goLogger) Infof(format string, args ...any) {
	cacheNotice(format, args...)
}

func (goLogger) Warnf(format string, args ...any) {
	cacheNotice(format, args...)
}

func (goLogger) Debugf(string, ...any) {}
