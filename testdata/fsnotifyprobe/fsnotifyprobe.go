// fsnotifyprobe runs the real github.com/fsnotify/fsnotify library. The runtime
// probe makes the inotify calls itself. This program goes through the library's
// own descriptor, reads and record parsing, which is what a consumer runs.
//
// It prints "ok <name>" per check and "ok all" last. The first failure prints
// "FAIL <name>: <why>" and exits 1.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// eventWait bounds how long one event may take to arrive.
const eventWait = 10 * time.Second

func fail(name, format string, args ...any) {
	fmt.Printf("FAIL %s: %s\n", name, fmt.Sprintf(format, args...))
	os.Exit(1)
}

func must(name string, err error) {
	if err != nil {
		fail(name, "%v", err)
	}
	fmt.Println("ok " + name)
}

// await reads events until one carries op on path. Any watcher error fails the
// check, and so does a closed channel or a wait that runs out.
func await(watcher *fsnotify.Watcher, check string, op fsnotify.Op, path string) {
	timeout := time.After(eventWait)
	var seen []string
	for {
		select {
		case event, open := <-watcher.Events:
			if !open {
				fail(check, "the events channel closed before %s on %s; saw %q", op, path, seen)
			}
			if event.Has(op) && event.Name == path {
				fmt.Printf("ok %s: %s\n", check, event)
				return
			}
			seen = append(seen, event.String())
		case err, open := <-watcher.Errors:
			if !open {
				fail(check, "the errors channel closed before %s on %s; saw %q", op, path, seen)
			}
			fail(check, "watcher error: %v", err)
		case <-timeout:
			fail(check, "no %s on %s within %s; saw %q", op, path, eventWait, seen)
		}
	}
}

func main() {
	dir, err := os.MkdirTemp("", "fsnotifyprobe")
	if err != nil {
		fail("mkdirtemp", "%v", err)
	}
	defer os.RemoveAll(dir)

	watcher, err := fsnotify.NewWatcher()
	must("newwatcher", err)
	must("add", watcher.Add(dir))

	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")

	if err := os.WriteFile(first, nil, 0o600); err != nil {
		fail("create", "%v", err)
	}
	await(watcher, "create", fsnotify.Create, first)

	if err := os.WriteFile(first, []byte("text"), 0o600); err != nil {
		fail("write", "%v", err)
	}
	await(watcher, "write", fsnotify.Write, first)

	if err := os.Rename(first, second); err != nil {
		fail("rename", "%v", err)
	}
	await(watcher, "rename", fsnotify.Rename, first)
	await(watcher, "renamed", fsnotify.Create, second)

	if err := os.Remove(second); err != nil {
		fail("remove", "%v", err)
	}
	await(watcher, "remove", fsnotify.Remove, second)

	must("close", watcher.Close())
	fmt.Println("ok all")
}
