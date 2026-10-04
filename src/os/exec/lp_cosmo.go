// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package exec

import (
	"errors"
	"internal/runtime/syscall/cosmo"
	"internal/syscall/unix"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// A GOOS=cosmo binary is one image that runs on Linux, macOS, and Windows hosts.

// ErrNotFound is the error resulting if a path search failed to find an executable file.
var ErrNotFound = errors.New("executable file not found in $PATH")

// ntHost reports whether this process is running on a Windows (NT)
// host. Constant after boot.
func ntHost() bool {
	return cosmo.Windows() != nil
}

// findExecutable is the unix-host check, verbatim from lp_unix.go.
func findExecutable(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	m := d.Mode()
	if m.IsDir() {
		return syscall.EISDIR
	}
	err = unix.Eaccess(file, unix.X_OK)
	// ENOSYS means Eaccess is not available or not implemented. EPERM can be
	// returned by Linux containers employing seccomp.
	if err == nil || (err != syscall.ENOSYS && err != syscall.EPERM) {
		return err
	}
	if m&0111 != 0 {
		return nil
	}
	return fs.ErrPermission
}

func lookPath(file string) (string, error) {
	if ntHost() {
		return ntLookPath(file)
	}

	// Unix host: verbatim lp_unix.go semantics.

	if err := validateLookPath(file); err != nil {
		return "", &Error{file, err}
	}

	if strings.Contains(file, "/") {
		err := findExecutable(file)
		if err == nil {
			return file, nil
		}
		return "", &Error{file, err}
	}
	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			// Unix shell semantics: path element "" means "."
			dir = "."
		}
		path := filepath.Join(dir, file)
		if err := findExecutable(path); err == nil {
			if !filepath.IsAbs(path) {
				if execerrdot.Value() != "0" {
					return path, &Error{file, ErrDot}
				}
				execerrdot.IncNonDefault()
			}
			return path, nil
		}
	}
	return "", &Error{file, ErrNotFound}
}

// lookExtensions is a no-op on unix hosts, since they do not restrict
// executables to specific extensions.
func lookExtensions(path, dir string) (string, error) {
	if ntHost() {
		return ntLookExtensions(path, dir)
	}
	return path, nil
}

// lookExtensionsEnabled reports whether Command/Start must route paths
// through lookExtensions (see exec.go): only on NT hosts.
func lookExtensionsEnabled() bool {
	return ntHost()
}

// ---- NT host implementation (port of lp_windows.go) ----

func ntIsDriveLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func ntLookupEnv(name string) (string, bool) {
	value, found := "", false
	for _, kv := range os.Environ() {
		if eq := strings.IndexByte(kv, '='); eq > 0 && strings.EqualFold(kv[:eq], name) {
			value, found = kv[eq+1:], true
		}
	}
	return value, found
}

func ntGetenv(name string) string {
	value, _ := ntLookupEnv(name)
	return value
}

func ntChkStat(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	if d.IsDir() {
		return fs.ErrPermission
	}
	return nil
}

func ntHasExt(file string) bool {
	i := strings.LastIndex(file, ".")
	if i < 0 {
		return false
	}
	return strings.LastIndexAny(file, `:\/`) < i
}

// ntExt returns the extension of the last path element, tolerating
// both slash flavors and drive colons ("C:\a.d\b" has no extension).
func ntExt(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 || strings.LastIndexAny(path, `:\/`) > i {
		return ""
	}
	return path[i:]
}

// ntIsAbs reports whether path is absolute in any spelling the NT path layer
// accepts: cosmo-rooted ("/c/...", "/tmp/..."), drive-absolute ("C:\...",
// "c:/..."), or UNC ("\\host\share").
func ntIsAbs(path string) bool {
	if len(path) > 0 && path[0] == '/' {
		return true
	}
	if len(path) >= 3 && ntIsDriveLetter(path[0]) && path[1] == ':' &&
		(path[2] == '\\' || path[2] == '/') {
		return true
	}
	return len(path) >= 2 && path[0] == '\\' && path[1] == '\\'
}

func ntJoin(dir, file string) string {
	if dir == "" {
		return file
	}
	if c := dir[len(dir)-1]; c == '/' || c == '\\' ||
		(c == ':' && len(dir) == 2 && ntIsDriveLetter(dir[0])) {
		return dir + file
	}
	if strings.ContainsRune(dir, '\\') ||
		(len(dir) >= 2 && dir[1] == ':' && ntIsDriveLetter(dir[0])) {
		return dir + `\` + file
	}
	return dir + "/" + file
}

// ntSplitList splits an NT PATH value on ';', respecting and
// stripping double quotes - the windows filepath.SplitList algorithm
// (cosmo's filepath is unix-flavored and would split on ':').
func ntSplitList(path string) []string {
	if path == "" {
		return nil
	}
	var list []string
	start := 0
	quo := false
	for i := 0; i < len(path); i++ {
		switch c := path[i]; {
		case c == '"':
			quo = !quo
		case c == ';' && !quo:
			list = append(list, path[start:i])
			start = i + 1
		}
	}
	list = append(list, path[start:])
	for i, s := range list {
		list[i] = strings.ReplaceAll(s, `"`, ``)
	}
	return list
}

func ntFindExecutable(file string, exts []string) (string, error) {
	if len(exts) == 0 {
		return file, ntChkStat(file)
	}
	if ntHasExt(file) {
		if ntChkStat(file) == nil {
			return file, nil
		}
	}
	for _, e := range exts {
		if f := file + e; ntChkStat(f) == nil {
			return f, nil
		}
	}
	if ntHasExt(file) {
		return "", fs.ErrNotExist
	}
	// Extensionless last resort: an existing bare file (an APE, say)
	// is accepted after every PATHEXT probe missed.
	if ntChkStat(file) == nil {
		return file, nil
	}
	return "", ErrNotFound
}

// ntPathExt is lp_windows.go's pathExt with the case-insensitive
// environment read.
func ntPathExt() []string {
	var exts []string
	x := ntGetenv("PATHEXT")
	if x != "" {
		for e := range strings.SplitSeq(strings.ToLower(x), `;`) {
			if e == "" {
				continue
			}
			if e[0] != '.' {
				e = "." + e
			}
			exts = append(exts, e)
		}
	} else {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	return exts
}

func ntLookPath(file string) (string, error) {
	if err := validateLookPath(file); err != nil {
		return "", &Error{file, err}
	}
	return ntLookPathExts(file, ntPathExt())
}

// ntLookPathExts implements LookPath on NT for the given PATHEXT
// list; the body is lp_windows.go's lookPathExts on the nt helpers.
func ntLookPathExts(file string, exts []string) (string, error) {
	if strings.ContainsAny(file, `:\/`) {
		f, err := ntFindExecutable(file, exts)
		if err == nil {
			return f, nil
		}
		return "", &Error{file, err}
	}

	// On Windows, creating the NoDefaultCurrentDirectoryInExePath environment
	// variable (with any value or no value!) signals.
	var (
		dotf   string
		dotErr error
	)
	if _, found := ntLookupEnv("NoDefaultCurrentDirectoryInExePath"); !found {
		if f, err := ntFindExecutable(file, exts); err == nil {
			if execerrdot.Value() == "0" {
				execerrdot.IncNonDefault()
				return f, nil
			}
			dotf, dotErr = f, &Error{file, ErrDot}
		}
	}

	path := ntGetenv("PATH")
	for _, dir := range ntSplitList(path) {
		if dir == "" {
			// Skip empty entries, consistent with what PowerShell does.
			continue
		}

		if f, err := ntFindExecutable(ntJoin(dir, file), exts); err == nil {
			if dotErr != nil {
				// https://go.dev/issue/53536: if we resolved a relative path implicitly.
				dotfi, dotfiErr := os.Lstat(dotf)
				fi, fiErr := os.Lstat(f)
				if dotfiErr != nil || fiErr != nil || !os.SameFile(dotfi, fi) {
					return dotf, dotErr
				}
			}

			if !ntIsAbs(f) {
				if execerrdot.Value() != "0" {
					// If this is the same relative path that we already found, dotErr is
					// non-nil and we already checked it above.
					if dotErr == nil {
						dotf, dotErr = f, &Error{file, ErrDot}
					}
					continue
				}
				execerrdot.IncNonDefault()
			}
			return f, nil
		}
	}

	if dotErr != nil {
		return dotf, dotErr
	}
	return "", &Error{file, ErrNotFound}
}

// ntLookExtensions is lp_windows.go's lookExtensions on the nt helpers:
// resolve the PATHEXT suffix for an explicit (pathed) program name without
// searching PATH.
//
// If the path already has an extension found in PATHEXT, it is returned
// directly without searching for additional extensions. For example,
// "C:\foo\example.com" would be returned as-is even if the program is
// "C:\foo\example.com.exe".
func ntLookExtensions(path, dir string) (string, error) {
	if err := validateLookPath(path); err != nil {
		return "", &Error{path, err}
	}

	if !strings.ContainsAny(path, `:\/`) {
		path = "./" + path
	}
	exts := ntPathExt()
	if ext := ntExt(path); ext != "" {
		for _, e := range exts {
			if strings.EqualFold(ext, e) {
				// Assume that path has already been resolved.
				return path, nil
			}
		}
	}
	if dir == "" {
		return ntLookPathExts(path, exts)
	}
	if len(path) >= 2 && ntIsDriveLetter(path[0]) && path[1] == ':' {
		// Drive-qualified: independent of dir.
		return ntLookPathExts(path, exts)
	}
	if len(path) > 1 && (path[0] == '/' || path[0] == '\\') {
		return ntLookPathExts(path, exts)
	}
	dirandpath := ntJoin(dir, path)
	// We assume that ntLookPathExts will only add file extension.
	lp, err := ntLookPathExts(dirandpath, exts)
	if err != nil {
		return "", err
	}
	ext := strings.TrimPrefix(lp, dirandpath)
	return path + ext, nil
}
