// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package cache

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"cmd/go/internal/base"
	goCfg "cmd/go/internal/cfg"

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
	// A test binary and a `go run` program are started with the environment this command was started with, rather than with this process's own.
	goCfg.OrigEnv = append(goCfg.OrigEnv[:len(goCfg.OrigEnv):len(goCfg.OrigEnv)], cacheclient.BrokerEnviron()...)
	// A go command that never runs a build still opens the cache, and a failing
	// one exits through base.Exit without returning.
	base.AtExit(func() {
		cacheclient.CloseStore()
		cacheclient.StopBroker()
	})
	return cache, nil
}

// The store is configured by the environment, and what the build IS comes from
// the build itself: the target it produces and the module it builds. The store
// has no other way to learn either, so its log would otherwise say how many
// objects moved and nothing about whose build moved them.

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
	cfg.Version = runtime.Version()
	cfg.Module = mainModulePath()
	// The key index's disk copy lives beside the cache it describes.
	cfg.IndexDir = dir
	cacheclient.SetLogger(goLogger{})
	store, err := cacheclient.NewWebBackend(cfg)
	if err != nil {
		// A store that cannot be reached is a slower build, not a broken one.
		fmt.Fprintf(os.Stderr, "go: shared build cache disabled: %v\n", err)
		return nil, nil
	}
	liveStore.Store(store)
	return store, nil
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

func (goLogger) Infof(format string, args ...any) {
	cacheNotice(format, args...)
}

func (goLogger) Warnf(format string, args ...any) {
	cacheNotice(format, args...)
}

func (goLogger) Debugf(string, ...any) {}
