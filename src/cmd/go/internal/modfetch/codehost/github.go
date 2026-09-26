// All rights reserved. Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codehost

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"cmd/go/internal/cfg"
	"cmd/go/internal/web"
)

// A githubRepo is a repository on github.com.
type githubRepo struct {
	owner, name string
}

var githubSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// parseGitHubRemote returns the repository that remote names. It accepts only
// https://github.com/<owner>/<repo>, with an optional .git suffix. Any other
// host is not trusted to serve the same bytes as the git remote.
func parseGitHubRemote(remote string) (githubRepo, bool) {
	u, err := url.Parse(remote)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return githubRepo{}, false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 2 {
		return githubRepo{}, false
	}
	owner, name := parts[0], strings.TrimSuffix(parts[1], ".git")
	for _, part := range []string{owner, name} {
		if !githubSegment.MatchString(part) || part == "." || part == ".." {
			return githubRepo{}, false
		}
	}
	return githubRepo{owner: owner, name: name}, true
}

// githubHost reports whether an archive download may touch host. github.com
// redirects every archive to codeload.github.com.
func githubHost(host string) bool {
	return host == "github.com" || host == "codeload.github.com"
}

// proxyHost is a fetch proxy that the owner of this fork runs. It reaches GitHub when a direct request cannot.
const proxyHost = "proxy.pazer.ai"

func isProxyHost(host string) bool { return host == proxyHost }

// An archiveSource is one place to download an archive from, and the hosts
// the request may touch on the way.
type archiveSource struct {
	url       string
	ext       string
	allowHost func(string) bool
}

// archiveSources lists where to get the archive of hash, in the order to try:
// the github.com archive URL, then that same URL through the proxy, for the
// tar.gz and then for the zip. A proxy request may not leave the proxy host,
// so a redirect that the proxy passes back is refused.
func (g githubRepo) archiveSources(ref, hash string) []archiveSource {
	name := refPath(ref, hash)
	var sources []archiveSource
	for _, ext := range []string{".tar.gz", ".zip"} {
		archive := "https://github.com/" + g.owner + "/" + g.name + "/archive/" + name + ext
		sources = append(sources,
			archiveSource{url: archive, ext: ext, allowHost: githubHost},
			archiveSource{url: "https://" + proxyHost + "/?url=" + url.QueryEscape(archive), ext: ext, allowHost: isProxyHost},
		)
	}
	return sources
}

// refPath names hash in an archive URL. A branch or a tag uses its ref name.
// HEAD has no ref path, so it uses the hash.
func refPath(ref, hash string) string {
	if !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/") {
		return hash
	}
	segments := strings.Split(ref, "/")
	for idx, seg := range segments {
		segments[idx] = url.PathEscape(seg)
	}
	return strings.Join(segments, "/")
}

// errNoGitHubArchive marks an archive that cannot stand in for git archive.
var errNoGitHubArchive = errors.New("archive cannot replace git")

// githubArchivePath is where a converted archive of hash is kept.
func (r *gitRepo) githubArchivePath(hash string) string {
	return filepath.Join(r.dir, "github", hash+".zip")
}

// statGitHub describes hash from an archive of ref, from the first source
// in archiveSources that works. The archive is kept on disk, so ReadFile and
// ReadZip never have to fetch the commit with git. It requires r.mu.
func (r *gitRepo) statGitHub(ctx context.Context, version, ref, hash string) (*RevInfo, error) {
	if r.github == nil || r.sha256Hashes || len(hash) != 40 {
		return nil, errNoGitHubArchive
	}
	if when, err := r.githubArchiveTime(hash); err == nil {
		return r.githubRevInfo(ctx, version, hash, when), nil
	}

	var errs []error
	for _, source := range r.github.archiveSources(ref, hash) {
		archive, when, err := r.downloadGitHub(ctx, source, hash)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := writeFileAtomic(r.githubArchivePath(hash), archive); err != nil {
			return nil, err
		}
		return r.githubRevInfo(ctx, version, hash, when), nil
	}
	return nil, errors.Join(errs...)
}

// downloadGitHub fetches one archive and converts it to the zip git archive
// writes. It returns the zip and the commit time.
func (r *gitRepo) downloadGitHub(ctx context.Context, source archiveSource, hash string) ([]byte, time.Time, error) {
	u, err := url.Parse(source.url)
	if err != nil {
		return nil, time.Time{}, err
	}
	ext := source.ext
	resp, err := web.GetPinned(u, source.allowHost)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if err := resp.Err(); err != nil {
		return nil, time.Time{}, err
	}
	body := &io.LimitedReader{R: resp.Body, N: MaxZipFile + 1}
	var archive []byte
	var when time.Time
	if ext == ".zip" {
		data, readErr := io.ReadAll(body)
		if readErr != nil {
			return nil, time.Time{}, fmt.Errorf("reading %s: %w", u.Redacted(), readErr)
		}
		if body.N <= 0 {
			return nil, time.Time{}, fmt.Errorf("%s: archive too large", u.Redacted())
		}
		archive, when, err = githubZipToArchive(data, hash)
	} else {
		archive, when, err = githubTarToArchive(body, hash)
		if err == nil && body.N <= 0 {
			err = fmt.Errorf("archive too large")
		}
	}
	if err != nil {
		err = fmt.Errorf("%s: %w", u.Redacted(), err)
		if xLog, ok := cfg.BuildXWriter(ctx); ok {
			fmt.Fprintf(xLog, "# github archive: %v\n", err)
		}
		return nil, time.Time{}, err
	}
	return archive, when, nil
}

// githubRevInfo builds what statLocal would return for hash, from the refs
// that ls-remote listed instead of the local repository.
func (r *gitRepo) githubRevInfo(ctx context.Context, version, hash string, when time.Time) *RevInfo {
	info := &RevInfo{
		Origin: &Origin{
			VCS:  "git",
			URL:  r.remoteURL,
			Hash: hash,
		},
		Name:    hash,
		Short:   r.shortenObjectHash(hash),
		Time:    when.UTC(),
		Version: hash,
	}
	if refs, err := r.loadRefs(ctx); err == nil {
		for ref, refHash := range refs {
			if tag, found := strings.CutPrefix(ref, "refs/tags/"); found && refHash == hash {
				info.Tags = append(info.Tags, tag)
			}
		}
	}
	slices.Sort(info.Tags)
	info.Tags = slices.Compact(info.Tags)
	for _, tag := range info.Tags {
		if version == tag {
			info.Version = version
		}
	}
	return info
}

// githubArchiveTime reports the commit time of the kept archive of hash.
func (r *gitRepo) githubArchiveTime(hash string) (time.Time, error) {
	if r.github == nil {
		return time.Time{}, fs.ErrNotExist
	}
	data, err := os.ReadFile(r.githubArchivePath(hash))
	if err != nil {
		return time.Time{}, err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return time.Time{}, err
	}
	return parseArchiveComment(reader.Comment, hash)
}

// githubArchive returns the kept archive of hash, or fs.ErrNotExist.
func (r *gitRepo) githubArchive(hash string) (*zip.Reader, []byte, error) {
	if r.github == nil {
		return nil, nil, fs.ErrNotExist
	}
	data, err := os.ReadFile(r.githubArchivePath(hash))
	if err != nil {
		return nil, nil, err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, err
	}
	if _, err := parseArchiveComment(reader.Comment, hash); err != nil {
		return nil, nil, err
	}
	return reader, data, nil
}

// readGitHubFile does for a kept archive what git cat-file blob does.
func readGitHubFile(reader *zip.Reader, file string) ([]byte, error) {
	name := archivePrefix + path.Clean(file)
	for _, entry := range reader.File {
		if entry.Name != name {
			continue
		}
		open, err := entry.Open()
		if err != nil {
			return nil, err
		}
		defer open.Close()
		return io.ReadAll(open)
	}
	return nil, fs.ErrNotExist
}

// subdirArchive does for a kept archive what a git archive pathspec does: it
// keeps the entries under subdir, and fails with fs.ErrNotExist when none is.
func subdirArchive(reader *zip.Reader, data []byte, subdir string) ([]byte, error) {
	dir := strings.Trim(subdir, "/")
	if dir == "" {
		return data, nil
	}
	under := archivePrefix + dir + "/"
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	kept := 0
	for _, entry := range reader.File {
		if !strings.HasPrefix(entry.Name, under) {
			continue
		}
		kept++
		dst, err := writer.CreateRaw(&entry.FileHeader)
		if err != nil {
			return nil, err
		}
		src, err := entry.OpenRaw()
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(dst, src); err != nil {
			return nil, err
		}
	}
	if kept == 0 {
		return nil, fs.ErrNotExist
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// An archiveBuilder turns the entries of a GitHub archive into the zip that
// ReadZip returns: one "prefix/" directory, files and symlinks only.
type archiveBuilder struct {
	hash    string
	top     string
	when    time.Time
	dirs    map[string]bool
	parents map[string]bool
	buf     bytes.Buffer
	writer  *zip.Writer
	files   int
}

func newArchiveBuilder(hash string) *archiveBuilder {
	builder := &archiveBuilder{
		hash:    hash,
		dirs:    make(map[string]bool),
		parents: make(map[string]bool),
	}
	builder.writer = zip.NewWriter(&builder.buf)
	return builder
}

// add records one entry. name is the path inside the archive, with its top
// directory. content is nil for a directory.
func (b *archiveBuilder) add(name string, mode fs.FileMode, mtime time.Time, content io.Reader) error {
	top, rest, found := strings.Cut(name, "/")
	if !found || top == "" {
		return fmt.Errorf("entry %q is outside a top-level directory", name)
	}
	if b.top == "" {
		b.top = top
	} else if top != b.top {
		return fmt.Errorf("archive has two top-level directories, %q and %q", b.top, top)
	}
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		return nil
	}
	if path.Clean(rest) != rest || strings.HasPrefix(rest, "../") || rest == ".." {
		return fmt.Errorf("entry %q has an unclean path", name)
	}
	if b.when.IsZero() {
		b.when = mtime
	} else if !mtime.Equal(b.when) {
		return fmt.Errorf("%w: entries carry different times, so no commit time", errNoGitHubArchive)
	}
	if parent := path.Dir(rest); parent != "." {
		b.parents[parent] = true
	}
	if mode.IsDir() {
		b.dirs[rest] = true
		return nil
	}
	if !mode.IsRegular() && mode&fs.ModeSymlink == 0 {
		return fmt.Errorf("entry %q has unsupported mode %v", name, mode)
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	if path.Base(rest) == ".gitattributes" {
		// cmd/go turns these attributes off before git archive runs, but
		// GitHub applies them. filter=lfs can swap a pointer for content.
		for _, attr := range []string{"export-ignore", "export-subst", "filter=lfs"} {
			if bytes.Contains(data, []byte(attr)) {
				return fmt.Errorf("%w: %s sets %s", errNoGitHubArchive, rest, attr)
			}
		}
	}
	header := &zip.FileHeader{Name: archivePrefix + rest, Method: zip.Deflate, Modified: mtime}
	header.SetMode(mode)
	dst, err := b.writer.CreateHeader(header)
	if err != nil {
		return err
	}
	if _, err := dst.Write(data); err != nil {
		return err
	}
	b.files++
	return nil
}

// finish returns the zip and the commit time.
func (b *archiveBuilder) finish() ([]byte, time.Time, error) {
	// A directory with nothing under it is a submodule: git has no other way to
	// write an empty directory.
	for dir := range b.dirs {
		if !b.parents[dir] {
			return nil, time.Time{}, fmt.Errorf("%w: %s is a submodule", errNoGitHubArchive, dir)
		}
	}
	if b.files == 0 {
		return nil, time.Time{}, fmt.Errorf("%w: archive holds no files", errNoGitHubArchive)
	}
	if err := b.writer.SetComment(b.hash + " " + strconv.FormatInt(b.when.Unix(), 10)); err != nil {
		return nil, time.Time{}, err
	}
	if err := b.writer.Close(); err != nil {
		return nil, time.Time{}, err
	}
	return b.buf.Bytes(), b.when, nil
}

// githubZipToArchive converts a git archive zip. Its comment names the
// commit, and every entry carries the commit time.
func githubZipToArchive(data []byte, hash string) ([]byte, time.Time, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, time.Time{}, err
	}
	if reader.Comment != hash {
		return nil, time.Time{}, fmt.Errorf("archive is of commit %q, want %s", reader.Comment, hash)
	}
	builder := newArchiveBuilder(hash)
	for _, entry := range reader.File {
		open, err := entry.Open()
		if err != nil {
			return nil, time.Time{}, err
		}
		err = builder.add(entry.Name, entry.Mode(), entry.Modified, open)
		open.Close()
		if err != nil {
			return nil, time.Time{}, err
		}
	}
	return builder.finish()
}

// parseArchiveComment reads the "<hash> <unix time>" comment of a kept archive.
func parseArchiveComment(comment, hash string) (time.Time, error) {
	id, sec, found := strings.Cut(comment, " ")
	if !found || id != hash {
		return time.Time{}, fmt.Errorf("kept archive is not of %s", hash)
	}
	unix, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("kept archive of %s has bad time %q", hash, sec)
	}
	return time.Unix(unix, 0).UTC(), nil
}

// writeFileAtomic writes data under a temporary name and renames it into
// place, so a concurrent reader never sees half an archive.
func writeFileAtomic(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o777); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".tmp*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
