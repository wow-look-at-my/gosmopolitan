// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package exec

// LookPath for GOOS=cosmo. One cosmo binary runs on Linux, macOS, and
// Windows NT, and "search PATH for an executable" means something
// different on each side of that split:
//
//   - unix hosts: ":"-separated PATH, extensionless names, the execute
//     permission bit decides executability (lp_unix.go's rules).
//   - NT hosts: ";"-separated PATH with drive-shaped entries
//     ("C:\Go\bin"), no execute bit, and the file must carry one of the
//     PATHEXT extensions (.exe, .com, ...; lp_windows.go's rules).
//
// GOOS=cosmo matches the unix build tag, so before this file existed
// LookPath was lp_unix.go on every host - and on NT it could never
// succeed: it split "C:\hostedtoolcache\...\bin" at the drive colon,
// probed for an extensionless name, and asked for +x. That regressed
// every PATH-based tool lookup when the fat APE's windows slot moved
// from an embedded native GOOS=windows payload (upstream lp_windows.go
// rules) to the cosmo payload on the NT personality - go-toolchain's
// smoke-windows step (`go-toolchain --help`) died in its go-bootstrap
// with "go not in PATH" on a runner whose PATH plainly held go.exe
// (go-toolchain run 29738066073; regression-pinned by
// testdata/runtimeprobe's "lookpath" check).
//
// The host is decided at run time via the NT emulation hook table
// (cosmo.Windows(), installed by the runtime at boot on NT hosts, nil
// elsewhere) - the same dispatch package syscall uses. The unix flavor
// below is lp_unix.go verbatim; the NT flavor ports lp_windows.go's
// PATHEXT search with the windows-only pieces it cannot use replaced:
//
//   - PATH and PATHEXT are read case-insensitively (ntGetenv): NT env
//     names are case-folded ("Path", "PATH", ...) and the spelling that
//     arrives depends on the parent process, while cosmo's unix-shaped
//     os.Getenv is case-sensitive.
//   - filepath is compiled with unix rules here, so ";"-list splitting
//     (quote-aware, ntSplitList), joining (ntJoin), and absoluteness
//     (ntIsAbs, which also accepts the path layer's rooted "/c/..."
//     spellings) are local helpers.
//   - The implicit current-directory probe (NoDefaultCurrentDirectoryIn
//     ExePath + ErrDot dance) is deliberately not ported: it exists to
//     reject cmd.exe's legacy CWD-first rule, and not searching the CWD
//     at all is the same end state with none of the machinery. PATH
//     entries naming "." still resolve through the loop, tagged with
//     ErrDot like any other relative result.
//
// Command/Start's lookExtensions extension-resolution paths stay gated
// on runtime.GOOS == "windows" (exec.go) and are unreachable here;
// exec.Command with a bare name goes through LookPath and gets the NT
// rules, while an explicit relative/absolute program path is used
// as given (spell out the extension). Candidate paths this file
// produces stat/execute fine on NT: the personality's path layer
// accepts drive-shaped spellings and flips separators (see
// runtime/os_cosmo_nt_path.go).

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

// ErrNotFound is the error resulting if a path search failed to find an executable file.
// (The "$PATH" spelling serves both hosts; on NT the variable is the
// same list under a case-folded name.)
var ErrNotFound = errors.New("executable file not found in $PATH")

// findExecutable is the unix-host executability check (lp_unix.go).
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
	// ENOSYS means Eaccess is not available or not implemented.
	// EPERM can be returned by Linux containers employing seccomp.
	// In both cases, fall back to checking the permission bits.
	if err == nil || (err != syscall.ENOSYS && err != syscall.EPERM) {
		return err
	}
	if m&0111 != 0 {
		return nil
	}
	return fs.ErrPermission
}

func lookPath(file string) (string, error) {
	if err := validateLookPath(file); err != nil {
		return "", &Error{file, err}
	}
	if cosmo.Windows() != nil {
		return ntLookPath(file)
	}
	return unixLookPath(file)
}

// unixLookPath is lp_unix.go's lookPath body, verbatim.
func unixLookPath(file string) (string, error) {
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

// lookExtensions is a no-op under GOOS=cosmo: the runtime.GOOS ==
// "windows" call sites in exec.go never run, and NT-host extension
// resolution for bare names happens inside ntLookPath instead.
func lookExtensions(path, dir string) (string, error) {
	return path, nil
}

// ntGetenv looks name up in the environment case-insensitively. NT
// environment names are case-folded and single-instance; the first
// match wins. Malformed block entries (leading '=') never match.
func ntGetenv(name string) string {
	for _, kv := range os.Environ() {
		if eq := strings.IndexByte(kv, '='); eq > 0 && strings.EqualFold(kv[:eq], name) {
			return kv[eq+1:]
		}
	}
	return ""
}

// ntPathExt is lp_windows.go's pathExt against the case-insensitive
// environment: the PATHEXT extension list, defaulting to the classic
// four.
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

// ntSplitList splits an NT PATH list: ";"-separated, with double
// quotes hiding separators and stripped from the result (the
// windows filepath.SplitList rules).
func ntSplitList(path string) []string {
	if path == "" {
		return nil
	}
	var list []string
	start, quo := 0, false
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
		list[i] = strings.ReplaceAll(s, `"`, "")
	}
	return list
}

// ntJoin joins a PATH entry and a file name with an NT separator
// (unless the entry already ends in one; both slash flavors count -
// the personality's path layer accepts either).
func ntJoin(dir, file string) string {
	if len(dir) > 0 {
		if c := dir[len(dir)-1]; c == '\\' || c == '/' {
			return dir + file
		}
	}
	return dir + `\` + file
}

// ntIsAbs reports whether an ntLookPath result names a fixed location:
// drive-absolute ("C:\x", either slash), UNC ("\\host\share"), or the
// cosmo path layer's rooted unix spellings ("/c/x", "/tmp/x", "\x") -
// treating rooted-on-current-drive as absolute deviates from
// filepath's windows IsAbs but keeps rooted PATH entries from being
// mis-tagged ErrDot.
func ntIsAbs(path string) bool {
	if len(path) >= 1 && (path[0] == '/' || path[0] == '\\') {
		return true
	}
	return len(path) >= 3 &&
		('a' <= path[0] && path[0] <= 'z' || 'A' <= path[0] && path[0] <= 'Z') &&
		path[1] == ':' &&
		(path[2] == '\\' || path[2] == '/')
}

// ntChkStat is lp_windows.go's chkStat: on NT existence decides
// executability (no mode bits), a directory is not runnable.
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

// ntHasExt is lp_windows.go's hasExt.
func ntHasExt(file string) bool {
	i := strings.LastIndex(file, ".")
	if i < 0 {
		return false
	}
	return strings.LastIndexAny(file, `:\/`) < i
}

// ntFindExecutable is lp_windows.go's findExecutable.
func ntFindExecutable(file string, exts []string) (string, error) {
	if len(exts) == 0 {
		return file, ntChkStat(file)
	}
	if ntHasExt(file) {
		if ntChkStat(file) == nil {
			return file, nil
		}
		// Keep checking exts below, so that programs with weird names
		// like "foo.bat.exe" will resolve instead of failing.
	}
	for _, e := range exts {
		if f := file + e; ntChkStat(f) == nil {
			return f, nil
		}
	}
	if ntHasExt(file) {
		return "", fs.ErrNotExist
	}
	return "", ErrNotFound
}

// ntLookPath implements LookPath with NT PATH semantics (lp_windows.go
// lookPathExts, minus the implicit current-directory probe - see the
// file comment).
func ntLookPath(file string) (string, error) {
	exts := ntPathExt()
	if strings.ContainsAny(file, `:\/`) {
		f, err := ntFindExecutable(file, exts)
		if err == nil {
			return f, nil
		}
		return "", &Error{file, err}
	}
	for _, dir := range ntSplitList(ntGetenv("PATH")) {
		if dir == "" {
			// Skip empty entries, consistent with what PowerShell does.
			continue
		}
		if f, err := ntFindExecutable(ntJoin(dir, file), exts); err == nil {
			if !ntIsAbs(f) {
				if execerrdot.Value() != "0" {
					return f, &Error{file, ErrDot}
				}
				execerrdot.IncNonDefault()
			}
			return f, nil
		}
	}
	return "", &Error{file, ErrNotFound}
}
