// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package orgmod

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// A go command that another go command starts builds the heads its ancestor
// resolved, so one go generate resolves each head once. No variable and no
// flag carries a head. The child finds a live ancestor that runs this same go
// executable, and reads what that process wrote under StateDir. To forge one,
// a process must write into the module cache, and that already changes code.

// StateDir returns the directory for the files this package keeps. It holds
// the heads a go command passes to its children, and a CI job's copy of its
// run locks. "" keeps nothing. modload points it into GOMODCACHE.
var StateDir = func() string { return "" }

// A procID names one process for its whole life. The start time tells a
// recycled pid from the process that held it before.
type procID struct {
	pid   int
	start string
}

func (id procID) String() string { return strconv.Itoa(id.pid) + "-" + id.start }

// procStat reads the parent pid and the start time of a process from /proc.
func procStat(pid string) (ppid int, start string, ok bool) {
	data, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return 0, "", false
	}
	stat := string(data)
	shut := strings.LastIndexByte(stat, ')')
	if shut < 0 {
		return 0, "", false
	}
	// The fields after the name start at the state. The parent pid is the
	// second of them, and the start time is the twentieth.
	fields := strings.Fields(stat[shut+1:])
	if len(fields) < 20 {
		return 0, "", false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", false
	}
	return ppid, fields[19], true
}

// self is this process. Where /proc is absent it is not found, and this
// process then passes no heads and takes none.
var self = sync.OnceValues(func() (procID, bool) {
	_, start, ok := procStat("self")
	return procID{os.Getpid(), start}, ok
})

// goAncestors returns the live ancestors of this process that run this same
// executable, the nearest first.
var goAncestors = sync.OnceValue(func() []procID {
	me, err := os.Stat("/proc/self/exe")
	if err != nil {
		return nil
	}
	var found []procID
	pid := os.Getppid()
	for depth := 0; pid > 1 && depth < 64; depth++ {
		name := strconv.Itoa(pid)
		ppid, start, ok := procStat(name)
		if !ok {
			break
		}
		if exe, err := os.Stat("/proc/" + name + "/exe"); err == nil && os.SameFile(me, exe) {
			found = append(found, procID{pid, start})
		}
		pid = ppid
	}
	return found
})

// headsStore returns the store that holds the heads owner passes on.
func headsStore(owner procID) (fileStore, bool) {
	root := StateDir()
	if root == "" {
		return fileStore{}, false
	}
	dir := filepath.Join(root, "heads", owner.String())
	return fileStore{raw: dir, dir: dir}, true
}

// InheritedHead returns the version a live go ancestor built for module on
// branch. The nearest ancestor that holds one answers.
func InheritedHead(module, branch string) (string, bool) {
	for _, owner := range goAncestors() {
		store, ok := headsStore(owner)
		if !ok {
			return "", false
		}
		version, found, err := store.Lookup(context.Background(), RunLockKey{Module: module, Branch: branch})
		if err == nil && found && version != "" {
			return version, true
		}
	}
	return "", false
}

var passedHeads sync.Once

// PassHead records the version this process builds for module on branch, for
// the go commands it starts. The first call returns the cleanup that removes
// the record when this process exits. Later calls return nil.
func PassHead(module, branch, version string) (cleanup func(), err error) {
	me, ok := self()
	if !ok {
		return nil, nil
	}
	store, ok := headsStore(me)
	if !ok {
		return nil, nil
	}
	if _, err := store.Claim(context.Background(), RunLockKey{Module: module, Branch: branch}, version); err != nil {
		return nil, err
	}
	passedHeads.Do(func() {
		cleanup = func() { os.RemoveAll(store.dir) }
	})
	return cleanup, nil
}
