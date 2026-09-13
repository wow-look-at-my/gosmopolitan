package submodulebranch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Module is one submodule this repository follows by branch.
type Module struct {
	Path string // where it sits, relative to the repository root
	URL  string
}

// Modules reads the entries of .gitmodules that carry `branch = .`. That value
// says the submodule follows the superproject's branch, which is the half git
// understands. A submodule without it keeps whatever commit is recorded.
func Modules(gitmodules []byte) []Module {
	var mods []Module
	var path, url string
	var follows bool
	commit := func() {
		if follows && path != "" && url != "" {
			mods = append(mods, Module{Path: path, URL: url})
		}
		path, url, follows = "", "", false
	}
	for _, line := range strings.Split(string(gitmodules), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			commit()
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "path":
			path = value
		case "url":
			url = value
		case "branch":
			follows = value == "."
		}
	}
	commit()
	return mods
}

// PseudoVersion is the version go.mod records for a commit of a module that
// publishes no tag: v0.0.0, the commit's own UTC time, and its short hash.
func PseudoVersion(run Runner, dir string) (string, error) {
	out, err := run("-C", dir, "show", "-s", "--format=%cd", "--date=format-local:%Y%m%d%H%M%S", "HEAD")
	if err != nil {
		return "", err
	}
	when := strings.TrimSpace(string(out))

	out, err = run("-C", dir, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("v0.0.0-%s-%s", when, strings.TrimSpace(string(out))), nil
}

// Follow moves one submodule onto the branch it follows and answers the
// version its new commit carries.
//
// A remote this cannot reach leaves the checkout alone. A build with no
// network then reads what it already has, rather than failing over a lookup
// it never needed.
func Follow(run Runner, root string, m Module, here string) (version string, err error) {
	ref, _ := Resolve(run, m.URL, here)
	dir := filepath.Join(root, m.Path)
	if _, err := run("-C", dir, "fetch", "--depth", "1", m.URL, ref); err != nil {
		return "", err
	}
	if _, err := run("-C", dir, "checkout", "--detach", "FETCH_HEAD"); err != nil {
		return "", err
	}
	return PseudoVersion(run, dir)
}

// Rewrite puts version on every line of a go.mod or a vendor/modules.txt that
// names module. Both files record the version, and the go command refuses to
// build in vendor mode when the two disagree.
func Rewrite(file, module, version string) error {
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	lines := strings.Split(string(src), "\n")
	changed := false
	for i, line := range lines {
		fields := strings.Fields(line)
		for j, f := range fields {
			if f != module || j+1 >= len(fields) {
				continue
			}
			if old := fields[j+1]; strings.HasPrefix(old, "v") && old != version {
				lines[i] = strings.Replace(line, old, version, 1)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644)
}
