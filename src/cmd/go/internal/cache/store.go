// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

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
// configured, and serves the result to the go commands this one starts.
func openCache(dir string) (Cache, error) {
	store, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	cache, err := cacheclient.OpenCache(dir, store)
	if err != nil {
		return nil, err
	}
	// A test binary and a `go run` program are started with the environment
	// this command was started with, rather than with this process's own, so
	// the socket has to be put in that one as well.
	goCfg.OrigEnv = append(goCfg.OrigEnv[:len(goCfg.OrigEnv):len(goCfg.OrigEnv)], cacheclient.BrokerEnviron()...)
	// A go command that never runs a build still opens the cache, and a
	// failing one exits through base.Exit without returning. So the exit gives
	// up the key index's lock and the socket, whatever else ran.
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
	// The key index's disk copy lives beside the cache it describes. Builds
	// that share GOCACHE then share one copy, whatever their TMPDIR. cmd/go's
	// script tests give every script its own TMPDIR, and each one fetched the
	// whole index for itself.
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

// sharedModule is the main module, which the module loader learns after this
// command has already opened its cache.
var sharedModule atomic.Pointer[string]

// SetSharedModule records which module's build is running, for the store's
// request headers. The module loader calls it once it knows, which is after
// the store is open, so an already-open store is told as well.
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
//
// A CACHE IN TROUBLE IS ALWAYS REPORTED. What is held back is the routine
// success reporting: the index size on every go command, and a summary per
// batch. Those say the cache is working, which the build does not need told,
// and there is one per go invocation or more.
//
// A go command's output is DATA to whoever ran it. Tests across this tree run
// `go list` and read the answer, so a routine line on that stream becomes a
// package name, a directory, or a file path somebody then opens. That is not
// hypothetical. It is what internal/godebugs, crypto/internal/fips140test and
// go/doc/comment did with it.
type goLogger struct{}

// CacheDebugEnv turns the routine success reporting back on. Anything but the
// empty string enables it.
const CacheDebugEnv = "GOCACHEDEBUG"

// CacheLogEnv names a file that takes the cache's notices in place of stderr.
// A build that compares the stderr of the go commands it runs sets it, as dist
// test does, and prints the file on its own stderr at the end. So an outage
// reaches the build's output and never a test's.
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
	if os.Getenv(CacheDebugEnv) == "" {
		return
	}
	cacheNotice(format, args...)
}

func (goLogger) Warnf(format string, args ...any) {
	cacheNotice(format, args...)
}

func (goLogger) Debugf(string, ...any) {}
