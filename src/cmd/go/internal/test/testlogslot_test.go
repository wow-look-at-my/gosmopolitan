// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package test

import (
	"bytes"
	"io"
	"testing"

	"cmd/go/internal/cache"

	"github.com/wow-look-at-my/go-s3-server/cacheclient/cachedisk"
)

// firstWriteStore keeps the first body put under a key and ignores every later
// one, as the shared store does.
type firstWriteStore struct{ *cachedisk.DiskCache }

func (store firstWriteStore) Put(id cache.ActionID, file io.ReadSeeker) (cache.OutputID, int64, error) {
	if entry, err := store.DiskCache.Get(id); err == nil {
		return entry.OutputID, entry.Size, nil
	}
	return store.DiskCache.Put(id, file)
}

func newFirstWriteStore(t *testing.T) firstWriteStore {
	disk, err := cachedisk.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disk.Close() })
	return firstWriteStore{disk}
}

func testlogOf(lines string) []byte {
	return append(append([]byte{}, testlogMagic...), lines...)
}

// TestTestlogsSurviveAWriteOnceStore: a testlog with new reads must reach the
// next lookup through a store that never replaces a key, and one of a shape
// already kept adds nothing.
func TestTestlogsSurviveAWriteOnceStore(t *testing.T) {
	store := newFirstWriteStore(t)
	var testID cache.ActionID
	testID[0] = 1

	first := testlogOf("open /a\n")
	second := testlogOf("open /a\nopen /b\n")
	again := testlogOf("open /a\n")

	saveTestlog(store, testID, first, testlogShape([]byte("open /a 01\n")), nil)
	saveTestlog(store, testID, second, testlogShape([]byte("open /a 01\nopen /b 02\n")), nil)
	// The same reads with other contents: the shape leaves out what each read found.
	saveTestlog(store, testID, again, testlogShape([]byte("open /a ff\n")), nil)

	got := savedTestlogs(store, testID)
	if len(got) != 2 {
		t.Fatalf("kept %d testlogs, want 2: %q", len(got), got)
	}
	if !bytes.Equal(got[0], second) || !bytes.Equal(got[1], first) {
		t.Fatalf("testlogs = %q, want the newest first: %q, %q", got, second, first)
	}
}

// TestTestlogSlotTakenByAnotherRun: a run that lost a slot to another run
// still saves its testlog, in the next free slot.
func TestTestlogSlotTakenByAnotherRun(t *testing.T) {
	store := newFirstWriteStore(t)
	var testID cache.ActionID
	testID[0] = 2

	other := testlogOf("open /other\n")
	mine := testlogOf("open /mine\n")
	cache.PutNoVerify(store, testlogSlotKey(testID, 0), bytes.NewReader(other))
	cache.PutNoVerify(store, testlogShapeKey(testID, 0), bytes.NewReader([]byte(testlogShape([]byte("open /other 01\n")))))

	saveTestlog(store, testID, mine, testlogShape([]byte("open /mine 01\n")), nil)
	got := savedTestlogs(store, testID)
	if len(got) != 2 || !bytes.Equal(got[0], mine) {
		t.Fatalf("testlogs = %q, want %q first", got, mine)
	}
}

// TestStaleTestlogOfTheSameShape: a kept testlog whose stored shape matches,
// but which names more inputs now, does not stop this run's testlog. The
// kept one lists an old temporary directory that is not scratch any more.
func TestStaleTestlogOfTheSameShape(t *testing.T) {
	store := newFirstWriteStore(t)
	var testID cache.ActionID
	testID[0] = 3

	stale := testlogOf("open /a\nopen /oldtmp/x\n")
	fresh := testlogOf("open /a\n")
	shape := testlogShape([]byte("open /a 01\n"))
	cache.PutNoVerify(store, testlogSlotKey(testID, 0), bytes.NewReader(stale))
	cache.PutNoVerify(store, testlogShapeKey(testID, 0), bytes.NewReader([]byte(shape)))

	shapeNow := func(kept []byte) string {
		if bytes.Equal(kept, stale) {
			return testlogShape([]byte("open /a 01\nopen /oldtmp/x 02\n"))
		}
		return shape
	}
	saveTestlog(store, testID, fresh, shape, shapeNow)
	got := savedTestlogs(store, testID)
	if len(got) != 2 || !bytes.Equal(got[0], fresh) {
		t.Fatalf("testlogs = %q, want %q first", got, fresh)
	}

	// The fresh testlog's shape now holds, so a repeat adds nothing.
	saveTestlog(store, testID, fresh, shape, shapeNow)
	if got := savedTestlogs(store, testID); len(got) != 2 {
		t.Fatalf("kept %d testlogs after a repeat, want 2", len(got))
	}
}
