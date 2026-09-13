package submodulebranch

import (
	"os"
	"path/filepath"
	"testing"
)

const gitmodules = `[submodule "testdata/fizzbuzz/test_helper/bats-assert"]
	path = testdata/fizzbuzz/test_helper/bats-assert
	url = https://github.com/bats-core/bats-assert.git
[submodule "src/cmd/vendor/github.com/wow-look-at-my/go-s3-server"]
	path = src/cmd/vendor/github.com/wow-look-at-my/go-s3-server
	url = https://github.com/wow-look-at-my/go-s3-server.git
	branch = .
[submodule "src/cmd/vendor/github.com/pierrec/lz4/v4"]
	path = src/cmd/vendor/github.com/pierrec/lz4/v4
	url = https://github.com/pierrec/lz4.git
`

// Only an entry that asks to follow is followed. A third-party submodule keeps
// the commit it records.
func TestModulesReadsOnlyTheFollowers(t *testing.T) {
	mods := Modules([]byte(gitmodules))
	if len(mods) != 1 {
		t.Fatalf("got %d entries, want the one that follows: %+v", len(mods), mods)
	}
	if mods[0].Path != "src/cmd/vendor/github.com/wow-look-at-my/go-s3-server" {
		t.Fatalf("path %q", mods[0].Path)
	}
	if mods[0].URL != "https://github.com/wow-look-at-my/go-s3-server.git" {
		t.Fatalf("url %q", mods[0].URL)
	}
}

// A branch naming an actual branch is a deliberate choice, not a follow.
func TestModulesIgnoresANamedBranch(t *testing.T) {
	named := "[submodule \"x\"]\n\tpath = x\n\turl = u\n\tbranch = release\n"
	if mods := Modules([]byte(named)); len(mods) != 0 {
		t.Fatalf("got %+v, want none", mods)
	}
}

func TestPseudoVersion(t *testing.T) {
	run := func(args ...string) ([]byte, error) {
		if args[2] == "show" {
			return []byte("20260912175122\n"), nil
		}
		return []byte("27b72ef4fa35\n"), nil
	}
	got, err := PseudoVersion(run, "d")
	if err != nil {
		t.Fatal(err)
	}
	if want := "v0.0.0-20260912175122-27b72ef4fa35"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// go.mod and vendor/modules.txt both record the version, and the go command
// refuses to build in vendor mode when they disagree.
func TestRewriteMovesBothFiles(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "go.mod")
	txt := filepath.Join(dir, "modules.txt")
	const path = "github.com/wow-look-at-my/go-s3-server/cacheclient"
	if err := os.WriteFile(mod, []byte("require (\n\t"+path+" v0.0.0-20260911210245-00f6b5d658c0\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(txt, []byte("# "+path+" v0.0.0-20260911210245-00f6b5d658c0\n## explicit; go 1.26\n"+path+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const next = "v0.0.0-20260912175122-27b72ef4fa35"
	for _, f := range []string{mod, txt} {
		if err := Rewrite(f, path, next); err != nil {
			t.Fatal(err)
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !contains(string(src), next) {
			t.Fatalf("%s did not take the version: %s", filepath.Base(f), src)
		}
		if contains(string(src), "00f6b5d658c0") {
			t.Fatalf("%s kept the old version: %s", filepath.Base(f), src)
		}
	}
}

// The bare package line naming the module must keep its shape: it carries no
// version, so there is nothing on it to replace.
func TestRewriteLeavesThePackageLineAlone(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "modules.txt")
	const path = "github.com/wow-look-at-my/go-s3-server/cacheclient"
	if err := os.WriteFile(txt, []byte(path+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Rewrite(txt, path, "v0.0.0-20260912175122-27b72ef4fa35"); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(txt)
	if err != nil {
		t.Fatal(err)
	}
	if string(src) != path+"\n" {
		t.Fatalf("got %q", src)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
