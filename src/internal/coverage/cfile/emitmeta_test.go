// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A forked test suite runs many processes of one binary against the directory
// their parent named, and each one emits meta-data under the same name. Windows
// refuses the rename while another process holds that file open. The file it
// refuses to overwrite is the file this run would have written, so the run has
// nothing left to do and must not fail.
func TestAMetaFileAnotherProcessAlreadyWroteIsNotAFailure(t *testing.T) {
	const content = "meta-data another process wrote"
	s := stateReadyToEmit(t, "covmeta.0123")
	if err := os.WriteFile(s.mfname, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	failEveryRename(t)

	if err := s.emitMetaDataFile([16]byte{}, uint64(len(content))); err != nil {
		t.Fatalf("a meta-data file already at the final name should not fail the emit: %v", err)
	}
	if _, err := os.Stat(s.mftmp); !os.IsNotExist(err) {
		t.Errorf("the temporary file should be gone once the emit gives up on it")
	}
	got, err := os.ReadFile(s.mfname)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("the file the other process wrote reads %q, and should read %q", got, content)
	}
}

// A rename that leaves nothing at the final name is still a failure. Recovering
// from every one of them would hide a real fault in the output directory.
func TestARenameThatLeavesNoMetaFileStillFails(t *testing.T) {
	s := stateReadyToEmit(t, "covmeta.4567")

	failEveryRename(t)

	if err := s.emitMetaDataFile([16]byte{}, 9); err == nil {
		t.Fatal("a rename that left nothing at the final name should fail the emit")
	}
}

// stateReadyToEmit returns a state whose temporary file is open, which is where
// openOutputFiles leaves one.
func stateReadyToEmit(t *testing.T, name string) *emitState {
	t.Helper()
	dir := t.TempDir()
	s := &emitState{
		outdir: dir,
		mfname: filepath.Join(dir, name),
		mftmp:  filepath.Join(dir, "tmp."+name),
	}
	f, err := os.Create(s.mftmp)
	if err != nil {
		t.Fatal(err)
	}
	s.mf = f
	return s
}

// failEveryRename makes every rename report what Windows reports when another
// process holds the destination open.
func failEveryRename(t *testing.T) {
	t.Helper()
	// The swap is process wide, so no test may run beside this one.
	t.Serial()
	previous := renameFile
	renameFile = func(string, string) error { return errors.New("Access is denied.") }
	t.Cleanup(func() { renameFile = previous })
}
