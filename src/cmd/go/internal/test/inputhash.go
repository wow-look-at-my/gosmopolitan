// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"cmd/go/internal/cache"
)

// fileHashMemo answers hashOpen, hashStat and the checks of where a name sits
// once per name for one testlog. A test can open the same file many times, and
// the log has a line for each. Each check resolves the symlinks of every
// directory above the name, which is an lstat per component. The lines still
// go into the inputs ID one by one, so the ID does not change.
type fileHashMemo struct {
	mu    sync.Mutex
	files map[string]*inputHash
}

type inputHash struct {
	once sync.Once
	id   cache.ActionID
	err  error
	// found and rel are the answer of a check: scratch, or the module cache.
	found bool
	rel   string
}

func newFileHashMemo() *fileHashMemo {
	return &fileHashMemo{files: make(map[string]*inputHash)}
}

func (hs *fileHashMemo) entry(op, name string) *inputHash {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	key := op + " " + name
	entry := hs.files[key]
	if entry == nil {
		entry = new(inputHash)
		hs.files[key] = entry
	}
	return entry
}

func (hs *fileHashMemo) open(name string) (cache.ActionID, error) {
	entry := hs.entry("open", name)
	entry.once.Do(func() { entry.id, entry.err = hashOpen(name) })
	return entry.id, entry.err
}

func (hs *fileHashMemo) stat(name string) cache.ActionID {
	entry := hs.entry("stat", name)
	entry.once.Do(func() { entry.id = hashStat(name) })
	return entry.id
}

// scratch is isRunScratch(name) || isBuildCache(name): the name is no input.
func (hs *fileHashMemo) scratch(name string) bool {
	entry := hs.entry("scratch", name)
	entry.once.Do(func() { entry.found = isRunScratch(name) || isBuildCache(name) })
	return entry.found
}

// modCache is inModCache(name).
func (hs *fileHashMemo) modCache(name string) (string, bool) {
	entry := hs.entry("modcache", name)
	entry.once.Do(func() { entry.rel, entry.found = inModCache(name) })
	return entry.rel, entry.found
}

// prefetch checks and hashes, in parallel, every name the testlog opens or
// stats. A filesystem call on Windows costs a round trip to the driver, and a
// test that type-checks the tree reads thousands of files. The hash of a name
// a parser reported is left for the ID walk, which decides if it counts.
func (hs *fileHashMemo) prefetch(dir string, testlog []byte, parsed map[string]int) {
	type job struct{ op, name string }
	var jobs []job
	seen := make(map[string]bool)
	pwd := dir
	for _, line := range bytes.Split(testlog, []byte("\n")) {
		op, name, found := strings.Cut(string(line), " ")
		if !found {
			continue
		}
		if op == "chdir" {
			pwd = name
			continue
		}
		if op != "open" && op != "stat" {
			continue
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(pwd, name)
		}
		key := op + " " + name
		if seen[key] || isProcessLocal(name) {
			continue
		}
		seen[key] = true
		jobs = append(jobs, job{op, name})
	}
	if len(jobs) < 2 {
		return
	}
	work := make(chan job)
	var wg sync.WaitGroup
	for range min(len(jobs), 4*runtime.GOMAXPROCS(0)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for next := range work {
				if hs.scratch(next.name) {
					continue
				}
				if _, ok := hs.modCache(next.name); ok || parsed[next.op+" "+next.name] > 0 {
					continue
				}
				if next.op == "open" {
					hs.open(next.name)
				} else {
					hs.stat(next.name)
				}
			}
		}()
	}
	for _, next := range jobs {
		work <- next
	}
	close(work)
	wg.Wait()
}
