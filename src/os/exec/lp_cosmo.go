// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

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

// A GOOS=cosmo binary is one image that runs on Linux, macOS, and
// Windows hosts, so executable lookup must pick its semantics at run
// time. On unix hosts lookPath/lookExtensions below are the verbatim
// lp_unix.go behavior. On an NT host every assumption lp_unix bakes
// in is wrong twice over: the process PATH is the raw Windows
// environment block value (';'-separated, drive-letter colons,
// backslashes - the runtime's ntGoenvs decodes GetEnvironmentStringsW
// without touching values), so splitting on ':' shreds it, and the
// on-disk executables carry PATHEXT suffixes ("go" is "go.exe"), so
// probing the bare name finds nothing. The ntLookPath/ntLookExtensions
// half of this file is a port of lp_windows.go's semantics with three
// cosmo adaptations, each marked below:
//
//  1. Environment names are matched case-insensitively (PATH vs
//     "Path"). GOOS=windows reads env through the case-insensitive
//     OS API; cosmo's os.Getenv is the exact-match unix scan over
//     the verbatim NT block, which typically spells it "Path" - an
//     exact Getenv("PATH") comes back empty under pwsh/GHA.
//  2. Candidate paths may be Windows-shaped, cosmo-rooted ("/c/...",
//     "/tmp/..."), or mixed; the runtime's NT path layer (ntPathW)
//     accepts all of these, so the helpers here only need syntax
//     (join/abs/ext) that tolerates both slash flavors and drive
//     letters, not translation.
//  3. After the PATHEXT probes fail, an extensionless candidate that
//     exists is accepted as a last resort (GOOS=windows refuses it).
//     APE binaries are routinely extensionless, and CreateProcessW
//     runs a pathed extensionless image fine - refusing would break
//     exec.Command("./tool") spawns that already work on NT today.
//
// The host switch is cosmo.Windows(): non-nil exactly when the
// runtime installed the NT emulation table at boot (the same check
// package syscall dispatches on).

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
	if ntHost() {
		return ntLookPath(file)
	}

	// Unix host: verbatim lp_unix.go semantics.
	//
	// NOTE(rsc): I wish we could use the Plan 9 behavior here
	// (only bypass the path if file begins with / or ./ or ../)
	// but that would not match all the Unix shells.

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
// executables to specific extensions; on NT hosts it resolves PATHEXT
// suffixes exactly like GOOS=windows (see ntLookExtensions).
func lookExtensions(path, dir string) (string, error) {
	if ntHost() {
		return ntLookExtensions(path, dir)
	}
	return path, nil
}

// lookExtensionsEnabled reports whether Command/Start must route
// paths through lookExtensions (see exec.go): only on NT hosts, where
// on-disk executables carry PATHEXT suffixes.
func lookExtensionsEnabled() bool {
	return ntHost()
}

// ---- NT host implementation (port of lp_windows.go) ----

func ntIsDriveLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// ntLookupEnv is the case-insensitive environment lookup (cosmo
// adaptation 1). The last match wins, so an os.Setenv("PATH", ...)
// made by this process overrides a "Path" inherited in the NT block
// (cosmo's Setenv appends new exact-case keys; the block itself never
// contains case-duplicates).
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

// ntIsAbs reports whether path is absolute in any spelling the NT
// path layer accepts: cosmo-rooted ("/c/...", "/tmp/..."),
// drive-absolute ("C:\...", "c:/..."), or UNC ("\\host\share").
// A drive-relative "C:foo" is not absolute, matching path/filepath's
// windows IsAbs.
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

// ntJoin joins a PATH directory entry and a file name, picking the
// separator flavor the entry itself uses so Windows-shaped entries
// yield Windows-shaped results (cosmo adaptation 2; the syscall layer
// accepts either flavor, this is for the caller-visible result). A
// bare drive ("C:") is drive-relative and joins with no separator,
// matching path/filepath's windows Join.
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

// ntFindExecutable is lp_windows.go's findExecutable plus the
// extensionless last resort (cosmo adaptation 3).
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

	// On Windows, creating the NoDefaultCurrentDirectoryInExePath
	// environment variable (with any value or no value!) signals that
	// path lookups should skip the current directory.
	// In theory we are supposed to call NeedCurrentDirectoryForExePathW
	// "as the registry location of this environment variable can change"
	// but that seems exceedingly unlikely: it would break all users who
	// have configured their environment this way!
	// https://docs.microsoft.com/en-us/windows/win32/api/processenv/nf-processenv-needcurrentdirectoryforexepathw
	// See also go.dev/issue/43947.
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
			// (See https://go.dev/issue/61493#issuecomment-1649724826.)
			continue
		}

		if f, err := ntFindExecutable(ntJoin(dir, file), exts); err == nil {
			if dotErr != nil {
				// https://go.dev/issue/53536: if we resolved a relative path implicitly,
				// and it is the same executable that would be resolved from the explicit %PATH%,
				// prefer the explicit name for the executable (and, likely, no error) instead
				// of the equivalent implicit name with ErrDot.
				//
				// Otherwise, return the ErrDot for the implicit path as soon as we find
				// out that the explicit one doesn't match.
				dotfi, dotfiErr := os.Lstat(dotf)
				fi, fiErr := os.Lstat(f)
				if dotfiErr != nil || fiErr != nil || !os.SameFile(dotfi, fi) {
					return dotf, dotErr
				}
			}

			if !ntIsAbs(f) {
				if execerrdot.Value() != "0" {
					// If this is the same relative path that we already found,
					// dotErr is non-nil and we already checked it above.
					// Otherwise, record this path as the one to which we must resolve,
					// with or without a dotErr.
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

// ntLookExtensions is lp_windows.go's lookExtensions on the nt
// helpers: resolve the PATHEXT suffix for an explicit (pathed)
// program name without searching PATH.
//
// If the path already has an extension found in PATHEXT,
// it is returned directly without searching for additional
// extensions. For example, "C:\foo\example.com" would be returned
// as-is even if the program is actually "C:\foo\example.com.exe".
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
