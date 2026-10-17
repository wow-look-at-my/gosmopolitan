// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
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
	sum   [sha256.Size]byte
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

// slowInputsNotice is how long a test's input hashing runs before the go
// command names its cost in a cache notice.
const slowInputsNotice = 2 * time.Second

// counts answers how many distinct names this memo opened and stated.
func (hs *fileHashMemo) counts() (opens, stats int) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	for key := range hs.files {
		switch {
		case strings.HasPrefix(key, "open "):
			opens++
		case strings.HasPrefix(key, "stat "):
			stats++
		}
	}
	return opens, stats
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

// pathKinds holds the scratch and module cache checks for the whole process.
// One check resolves every directory above a name, and each testlog kept for
// each package names the same tree. The key carries the directories the check
// reads, so a test that moves them gets new answers. GOCACHE is read once per
// process.
var pathKinds = newFileHashMemo()

// scratch is isRunScratch(name) || isBuildCache(name): the name is no input.
func (hs *fileHashMemo) scratch(name string) bool {
	entry := pathKinds.entry("scratch "+os.TempDir(), name)
	entry.once.Do(func() { entry.found = isRunScratch(name) || isBuildCache(name) })
	return entry.found
}

// modCache is inModCache(name).
func (hs *fileHashMemo) modCache(name string) (string, bool) {
	entry := pathKinds.entry("modcache "+cfg.GOMODCACHE, name)
	entry.once.Do(func() { entry.rel, entry.found = inModCache(name) })
	return entry.rel, entry.found
}

// contentSums holds the digest of each regular file hashOpen has read in this
// process, under its name, size and mtime. A test that runs go opens the whole
// toolchain binary, and every testlog of every package names it.
var contentSums = newFileHashMemo()

// contentSum is the SHA-256 of the regular file name, whose stat is info. The
// digest is kept only when a stat after the read still matches info.
func contentSum(name string, info fs.FileInfo) ([sha256.Size]byte, error) {
	key := fmt.Sprintf("%d %d", info.Size(), info.ModTime().UnixNano())
	entry := contentSums.entry(key, name)
	entry.once.Do(func() {
		entry.sum, entry.err = readSum(name)
		after, err := os.Stat(name)
		if entry.err == nil && (err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime())) {
			entry.err = errFileMoved
		}
	})
	if entry.err == errFileMoved {
		// The file changed while it was read. Read it again, and keep nothing.
		return readSum(name)
	}
	return entry.sum, entry.err
}

var errFileMoved = errors.New("file changed while it was hashed")

func readSum(name string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	file, err := os.Open(name)
	if err != nil {
		return sum, err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return sum, err
	}
	h.Sum(sum[:0])
	return sum, nil
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
