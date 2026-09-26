// All rights reserved. Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codehost

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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

	"golang.org/x/mod/semver"
)

// A githubRepo is a repository on github.com.
type githubRepo struct {
	owner, name string
}

var githubSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// githubRemote names the GitHub repository of a git remote. Tests replace it
// to put a local remote behind a fake github.com.
var githubRemote = parseGitHubRemote

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

// An archiveSource is one place to download from, and the hosts the request
// may touch on the way. credentialFor names the URL whose GOAUTH credential
// the request carries, when that is not url itself.
type archiveSource struct {
	url           string
	ext           string
	allowHost     func(string) bool
	credentialFor string
}

// viaProxy returns the source that fetches target through the proxy, with
// target's own credential, which the proxy forwards.
func viaProxy(target, ext string) archiveSource {
	return archiveSource{url: "https://" + proxyHost + "/?url=" + url.QueryEscape(target), ext: ext, allowHost: isProxyHost, credentialFor: target}
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
			viaProxy(archive, ext),
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

func isAPIHost(host string) bool { return host == "api.github.com" }

// maxAPIResponse bounds one page of an api.github.com list. A longer body
// fails to decode, so the caller falls back to ls-remote.
const maxAPIResponse = 16 << 20

// gsmHost is github-state-mirror, a cache of the GitHub API that the owner
// of this fork runs. It answers only a request that carries a GitHub token.
const gsmHost = "github-state-mirror.pazer.io"

func isGSMHost(host string) bool { return host == gsmHost }

// githubRefs returns what ls-remote would list, over plain HTTP. The first
// source that answers wins: the info/refs advertisement from github.com,
// direct and then through the proxy; then the REST API, from
// github-state-mirror, api.github.com, and api.github.com through the proxy.
func (r *gitRepo) githubRefs(ctx context.Context) (map[string]string, error) {
	infoRefs := "https://github.com/" + r.github.owner + "/" + r.github.name + ".git/info/refs?service=git-upload-pack"
	var errs []error
	for _, source := range []archiveSource{
		{url: infoRefs, allowHost: githubHost},
		viaProxy(infoRefs, ""),
	} {
		refs, err := r.githubInfoRefs(source)
		if err == nil {
			return refs, nil
		}
		errs = append(errs, err)
	}
	refs, err := r.githubAPIRefs(ctx)
	if err == nil {
		return refs, nil
	}
	errs = append(errs, err)
	err = errors.Join(errs...)
	if xLog, ok := cfg.BuildXWriter(ctx); ok {
		fmt.Fprintf(xLog, "# github refs: %v\n", err)
	}
	return nil, err
}

// githubInfoRefs reads the ref advertisement git itself fetches first.
func (r *gitRepo) githubInfoRefs(source archiveSource) (map[string]string, error) {
	u, err := url.Parse(source.url)
	if err != nil {
		return nil, err
	}
	resp, err := web.GetPinned(u, source.allowHost, source.credentialFor)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := resp.Err(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", u.Redacted(), err)
	}
	refs, err := parseRefAdvertisement(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u.Redacted(), err)
	}
	return refs, nil
}

// parseRefAdvertisement reads a smart-HTTP upload-pack advertisement: pkt-lines
// of "<hash> <ref>", the first with capabilities after a NUL. It keeps what
// loadRefs keeps, HEAD, heads and tags, with each annotated tag peeled.
func parseRefAdvertisement(data []byte) (map[string]string, error) {
	all := make(map[string]string)
	sawService := false
	for len(data) > 0 {
		if len(data) < 4 {
			return nil, errors.New("truncated pkt-line")
		}
		size, err := strconv.ParseUint(string(data[:4]), 16, 16)
		if err != nil {
			return nil, fmt.Errorf("bad pkt-line length %q", data[:4])
		}
		if size == 0 {
			data = data[4:]
			continue
		}
		if size < 4 || int(size) > len(data) {
			return nil, fmt.Errorf("bad pkt-line length %d", size)
		}
		line := strings.TrimSuffix(string(data[4:size]), "\n")
		data = data[size:]
		if strings.HasPrefix(line, "# service=") {
			sawService = true
			continue
		}
		line, _, _ = strings.Cut(line, "\x00")
		hash, ref, found := strings.Cut(line, " ")
		if !found || len(hash) != 40 || !AllHex(hash) {
			return nil, fmt.Errorf("bad ref line %q", line)
		}
		all[ref] = hash
	}
	if !sawService {
		return nil, errors.New("not a git upload-pack advertisement")
	}
	refs := make(map[string]string)
	for ref, hash := range all {
		if ref == "HEAD" || strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/tags/") {
			refs[ref] = hash
		}
	}
	for ref, hash := range refs {
		if name, peeled := strings.CutSuffix(ref, "^{}"); peeled {
			refs[name] = hash
			delete(refs, ref)
		}
	}
	return refs, nil
}

// githubAPIRefs lists every tag and branch at its commit, and HEAD at the
// default branch, from the REST API.
func (r *gitRepo) githubAPIRefs(ctx context.Context) (map[string]string, error) {
	type named struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	refs := make(map[string]string)
	for _, kind := range []string{"tags", "branches"} {
		prefix := "refs/tags/"
		if kind == "branches" {
			prefix = "refs/heads/"
		}
		for page := 1; ; page++ {
			var list []named
			if err := r.githubAPI(ctx, "/"+kind+"?per_page=100&page="+strconv.Itoa(page), &list); err != nil {
				return nil, err
			}
			for _, item := range list {
				refs[prefix+item.Name] = item.Commit.SHA
			}
			if len(list) < 100 {
				break
			}
		}
	}
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := r.githubAPI(ctx, "", &repo); err != nil {
		return nil, err
	}
	if hash, found := refs["refs/heads/"+repo.DefaultBranch]; found {
		refs["HEAD"] = hash
	}
	return refs, nil
}

// githubAPI decodes the JSON at /repos/<owner>/<repo><suffix>, from
// github-state-mirror, api.github.com, then api.github.com through the proxy.
func (r *gitRepo) githubAPI(ctx context.Context, suffix string, out any) error {
	route := "/repos/" + r.github.owner + "/" + r.github.name + suffix
	direct := "https://api.github.com" + route
	sources := []archiveSource{
		// The mirror wants the same GitHub token api.github.com takes.
		{url: "https://" + gsmHost + route, allowHost: isGSMHost, credentialFor: direct},
		{url: direct, allowHost: isAPIHost},
		viaProxy(direct, ""),
	}
	var errs []error
	for _, source := range sources {
		u, err := url.Parse(source.url)
		if err != nil {
			return err
		}
		resp, err := web.GetPinned(u, source.allowHost, source.credentialFor)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		err = resp.Err()
		if err == nil {
			err = json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponse)).Decode(out)
		}
		resp.Body.Close()
		if err == nil {
			return nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", u.Redacted(), err))
	}
	err := errors.Join(errs...)
	if xLog, ok := cfg.BuildXWriter(ctx); ok {
		fmt.Fprintf(xLog, "# github api: %v\n", err)
	}
	return err
}

<<<<<<< HEAD
// githubArchivePath is where the archive of hash is kept, byte for byte as
// GitHub served it, under ext (".tar.gz" or ".zip"). hash+".time" holds the
// commit time and is written last, so it marks a complete archive.
func (r *gitRepo) githubArchivePath(hash, ext string) string {
	return filepath.Join(r.dir, "github", hash+ext)
}

// keepGitHub stores a downloaded archive and remembers its parsed entries.
func (r *gitRepo) keepGitHub(hash, ext string, raw []byte, entries []archiveEntry, when time.Time) error {
	if err := writeFileAtomic(r.githubArchivePath(hash, ext), raw); err != nil {
		return err
	}
	if err := writeFileAtomic(r.githubArchivePath(hash, ".time"), []byte(strconv.FormatInt(when.Unix(), 10)+"\n")); err != nil {
		return err
	}
	r.githubMu.Lock()
	defer r.githubMu.Unlock()
	if r.githubFiles == nil {
		r.githubFiles = make(map[string][]archiveEntry)
	}
	r.githubFiles[hash] = entries
	return nil
}

// githubEntries returns the files of the kept archive of hash. It parses the
// archive once per process.
func (r *gitRepo) githubEntries(hash string) ([]archiveEntry, error) {
	if r.github == nil {
		return nil, fs.ErrNotExist
	}
	r.githubMu.Lock()
	defer r.githubMu.Unlock()
	if entries, found := r.githubFiles[hash]; found {
		return entries, nil
	}
	if _, err := r.githubArchiveTime(hash); err != nil {
		return nil, err
	}
	for _, ext := range []string{".tar.gz", ".zip"} {
		raw, err := os.ReadFile(r.githubArchivePath(hash, ext))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries, _, _, err := parseArchive(raw, ext, hash)
		if err != nil {
			return nil, err
		}
		if r.githubFiles == nil {
			r.githubFiles = make(map[string][]archiveEntry)
		}
		r.githubFiles[hash] = entries
		return entries, nil
	}
	return nil, fs.ErrNotExist
}

// parseArchive reads a GitHub archive into its entries, the commit time and
// the commit. hash stands in when the archive names no commit.
func parseArchive(raw []byte, ext, hash string) ([]archiveEntry, time.Time, string, error) {
	if ext == ".zip" {
		return parseZipArchive(raw, hash)
	}
	return parseTarGzArchive(bytes.NewReader(raw), hash)
=======
// githubArchivePath is where a converted archive of hash is kept.
func (r *gitRepo) githubArchivePath(hash string) string {
	return filepath.Join(r.dir, "github", hash+".zip")
>>>>>>> origin/master
}

// statGitHub describes hash from an archive of ref, from the first source
// in archiveSources that works. The archive is kept on disk, so ReadFile and
// ReadZip never have to fetch the commit with git. It requires r.mu.
func (r *gitRepo) statGitHub(ctx context.Context, version, ref, hash string) (*RevInfo, error) {
	if r.github == nil || r.sha256Hashes || len(hash) != 40 {
		return nil, errors.New("not a github.com repository with SHA-1 hashes")
	}
	if when, err := r.githubArchiveTime(hash); err == nil {
		return r.githubRevInfo(ctx, version, hash, when, nil), nil
	}

	var errs []error
	for _, source := range r.github.archiveSources(ref, hash) {
<<<<<<< HEAD
		raw, entries, when, _, err := r.downloadGitHub(ctx, source, hash)
=======
		archive, when, _, err := r.downloadGitHub(ctx, source, hash)
>>>>>>> origin/master
		if err != nil {
			errs = append(errs, err)
			continue
		}
<<<<<<< HEAD
		if err := r.keepGitHub(hash, source.ext, raw, entries, when); err != nil {
=======
		if err := writeFileAtomic(r.githubArchivePath(hash), archive); err != nil {
>>>>>>> origin/master
			return nil, err
		}
		return r.githubRevInfo(ctx, version, hash, when, nil), nil
	}
	return nil, errors.Join(errs...)
}

// isTagName reports whether rev names a version tag, such as v1.2.3 or
// sub/v1.2.3. Only such a rev takes the archive before ls-remote.
func isTagName(rev string) bool {
	return semver.IsValid(path.Base(rev)) && !strings.HasPrefix(rev, "refs/") && !strings.Contains(rev, "..")
}

// githubTagPath records which commit the kept archive of tag holds.
func (r *gitRepo) githubTagPath(tag string) string {
	return filepath.Join(r.dir, "github", "tags", filepath.FromSlash(tag))
}

// statGitHubTag describes tag from its archive alone. The archive names its
// commit, so ls-remote does not run. It requires r.mu.
func (r *gitRepo) statGitHubTag(ctx context.Context, tag string) (*RevInfo, error) {
	if r.github == nil || r.sha256Hashes {
		return nil, errors.New("not a github.com repository with SHA-1 hashes")
	}
	if data, err := os.ReadFile(r.githubTagPath(tag)); err == nil {
		hash := strings.TrimSpace(string(data))
		if when, err := r.githubArchiveTime(hash); err == nil {
			return r.githubTagInfo(ctx, tag, hash, when), nil
		}
	}

	var errs []error
	for _, source := range r.github.archiveSources("refs/tags/"+tag, "") {
<<<<<<< HEAD
		raw, entries, when, hash, err := r.downloadGitHub(ctx, source, "")
=======
		archive, when, hash, err := r.downloadGitHub(ctx, source, "")
>>>>>>> origin/master
		if err != nil {
			errs = append(errs, err)
			continue
		}
<<<<<<< HEAD
		if err := r.keepGitHub(hash, source.ext, raw, entries, when); err != nil {
=======
		if len(hash) != 40 || !AllHex(hash) {
			errs = append(errs, fmt.Errorf("%s: archive names no commit", source.url))
			continue
		}
		if err := writeFileAtomic(r.githubArchivePath(hash), archive); err != nil {
>>>>>>> origin/master
			return nil, err
		}
		if err := writeFileAtomic(r.githubTagPath(tag), []byte(hash+"\n")); err != nil {
			return nil, err
		}
		return r.githubTagInfo(ctx, tag, hash, when), nil
	}
	return nil, errors.Join(errs...)
}

func (r *gitRepo) githubTagInfo(ctx context.Context, tag, hash string, when time.Time) *RevInfo {
	info := r.githubRevInfo(ctx, tag, hash, when, []string{tag})
	info.Origin.Ref = "refs/tags/" + tag
	return info
}

<<<<<<< HEAD
// downloadGitHub fetches one archive and parses it. It returns the bytes as
// served, the entries, the commit time and the commit. hash is the commit when
// the archive does not name one.
func (r *gitRepo) downloadGitHub(ctx context.Context, source archiveSource, hash string) ([]byte, []archiveEntry, time.Time, string, error) {
	u, err := url.Parse(source.url)
	if err != nil {
		return nil, nil, time.Time{}, "", err
	}
	resp, err := web.GetPinned(u, source.allowHost, source.credentialFor)
	if err != nil {
		return nil, nil, time.Time{}, "", err
	}
	defer resp.Body.Close()
	if err := resp.Err(); err != nil {
		return nil, nil, time.Time{}, "", err
	}
	body := &io.LimitedReader{R: resp.Body, N: MaxZipFile + 1}
	raw, err := io.ReadAll(body)
	if err == nil && body.N <= 0 {
		err = errors.New("archive too large")
	}
	var entries []archiveEntry
	var when time.Time
	var commit string
	if err == nil {
		entries, when, commit, err = parseArchive(raw, source.ext, hash)
=======
// downloadGitHub fetches one archive and converts it to the zip git archive
// writes. It returns the zip, the commit time and the commit. hash is the
// commit when the archive does not name one.
func (r *gitRepo) downloadGitHub(ctx context.Context, source archiveSource, hash string) ([]byte, time.Time, string, error) {
	u, err := url.Parse(source.url)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	resp, err := web.GetPinned(u, source.allowHost, source.credentialFor)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	defer resp.Body.Close()
	if err := resp.Err(); err != nil {
		return nil, time.Time{}, "", err
	}
	body := &io.LimitedReader{R: resp.Body, N: MaxZipFile + 1}
	var archive []byte
	var when time.Time
	var commit string
	if source.ext == ".zip" {
		data, readErr := io.ReadAll(body)
		if readErr != nil {
			return nil, time.Time{}, "", fmt.Errorf("reading %s: %w", u.Redacted(), readErr)
		}
		if body.N <= 0 {
			return nil, time.Time{}, "", fmt.Errorf("%s: archive too large", u.Redacted())
		}
		archive, when, commit, err = githubZipToArchive(data, hash)
	} else {
		archive, when, commit, err = githubTarToArchive(body, hash)
		if err == nil && body.N <= 0 {
			err = fmt.Errorf("archive too large")
		}
>>>>>>> origin/master
	}
	if err != nil {
		err = fmt.Errorf("%s: %w", u.Redacted(), err)
		if xLog, ok := cfg.BuildXWriter(ctx); ok {
			fmt.Fprintf(xLog, "# github archive: %v\n", err)
		}
<<<<<<< HEAD
		return nil, nil, time.Time{}, "", err
	}
	return raw, entries, when, commit, nil
=======
		return nil, time.Time{}, "", err
	}
	return archive, when, commit, nil
>>>>>>> origin/master
}

// githubRevInfo builds what statLocal would return for hash. tags lists the
// tags on hash. When it is nil, they come from ls-remote.
func (r *gitRepo) githubRevInfo(ctx context.Context, version, hash string, when time.Time, tags []string) *RevInfo {
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
	info.Tags = tags
	if tags == nil {
		if refs, err := r.loadRefs(ctx); err == nil {
			for ref, refHash := range refs {
				if tag, found := strings.CutPrefix(ref, "refs/tags/"); found && refHash == hash {
					info.Tags = append(info.Tags, tag)
				}
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
<<<<<<< HEAD
	data, err := os.ReadFile(r.githubArchivePath(hash, ".time"))
	if err != nil {
		return time.Time{}, err
	}
	unix, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("kept archive of %s has bad time %q", hash, data)
	}
	return time.Unix(unix, 0).UTC(), nil
}

// A ModuleFile is one file of a revision, read without a zip.
type ModuleFile struct {
	Name string
	Mode fs.FileMode
	Data []byte
}

// A FileReader serves a revision's files directly. The module zip is then
// built from them, and it is the only zip.
type FileReader interface {
	ReadFiles(ctx context.Context, rev, subdir string) ([]ModuleFile, error)
}

// An archiveEntry is one file or symlink of an archive, named without the
// archive's top directory. A symlink's data is its target, as git cat-file
// prints it.
type archiveEntry struct {
	name string
	mode fs.FileMode
	data []byte
}

// readGitHubFile does for kept entries what git cat-file blob does.
func readGitHubFile(entries []archiveEntry, file string) ([]byte, error) {
	name := path.Clean(file)
	for _, entry := range entries {
		if entry.name == name {
			return entry.data, nil
		}
=======
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
>>>>>>> origin/master
	}
	return nil, fs.ErrNotExist
}

<<<<<<< HEAD
// subdirFiles returns the entries under subdir as module files, named from
// subdir. It fails with fs.ErrNotExist when there are none, as git archive
// does for a pathspec that matches nothing.
func subdirFiles(entries []archiveEntry, subdir string) ([]ModuleFile, error) {
	under := ""
	if dir := strings.Trim(subdir, "/"); dir != "" {
		under = dir + "/"
	}
	var files []ModuleFile
	for _, entry := range entries {
		if rest, found := strings.CutPrefix(entry.name, under); found {
			files = append(files, ModuleFile{Name: rest, Mode: entry.mode, Data: entry.data})
		}
	}
	if len(files) == 0 {
		return nil, fs.ErrNotExist
	}
	return files, nil
}

// entriesZip writes files as the zip ReadZip returns, under archivePrefix.
func entriesZip(files []ModuleFile, subdir string) ([]byte, error) {
	dir := strings.Trim(subdir, "/")
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, file := range files {
		name := archivePrefix + file.Name
		if dir != "" {
			name = archivePrefix + dir + "/" + file.Name
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(file.Mode)
		dst, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := dst.Write(file.Data); err != nil {
			return nil, err
		}
=======
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
>>>>>>> origin/master
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

<<<<<<< HEAD
// An archiveBuilder collects the entries of a GitHub archive: files and
// symlinks only, each named without the one top-level directory.
type archiveBuilder struct {
	top     string
	when    time.Time
	entries []archiveEntry
}

func newArchiveBuilder() *archiveBuilder {
	return &archiveBuilder{}
=======
// An archiveBuilder turns the entries of a GitHub archive into the zip that
// ReadZip returns: one "prefix/" directory, files and symlinks only.
type archiveBuilder struct {
	top    string
	when   time.Time
	buf    bytes.Buffer
	writer *zip.Writer
	files  int
}

func newArchiveBuilder() *archiveBuilder {
	builder := &archiveBuilder{}
	builder.writer = zip.NewWriter(&builder.buf)
	return builder
>>>>>>> origin/master
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
	}
	if mode.IsDir() {
		return nil
	}
	if !mode.IsRegular() && mode&fs.ModeSymlink == 0 {
		return fmt.Errorf("entry %q has unsupported mode %v", name, mode)
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
<<<<<<< HEAD
	b.entries = append(b.entries, archiveEntry{name: rest, mode: mode, data: data})
	return nil
}

// finish returns the entries, the commit time and the commit. embedded is the
// commit the archive names. hash stands in when the archive names none.
func (b *archiveBuilder) finish(embedded, hash string) ([]archiveEntry, time.Time, string, error) {
=======
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

// finish returns the zip, the commit time and the commit. embedded is the
// commit the archive names. hash stands in when the archive names none.
func (b *archiveBuilder) finish(embedded, hash string) ([]byte, time.Time, string, error) {
>>>>>>> origin/master
	commit := hash
	if len(embedded) == 40 && AllHex(embedded) {
		commit = embedded
	}
	if commit == "" {
		return nil, time.Time{}, "", errors.New("archive names no commit")
	}
<<<<<<< HEAD
	if len(b.entries) == 0 {
		return nil, time.Time{}, "", errors.New("archive holds no files")
	}
	return b.entries, b.when, commit, nil
}

// parseZipArchive reads a git archive zip. Its comment names the commit,
// and every entry carries the commit time.
func parseZipArchive(data []byte, hash string) ([]archiveEntry, time.Time, string, error) {
=======
	if b.files == 0 {
		return nil, time.Time{}, "", errors.New("archive holds no files")
	}
	if err := b.writer.SetComment(commit + " " + strconv.FormatInt(b.when.Unix(), 10)); err != nil {
		return nil, time.Time{}, "", err
	}
	if err := b.writer.Close(); err != nil {
		return nil, time.Time{}, "", err
	}
	return b.buf.Bytes(), b.when, commit, nil
}

// githubZipToArchive converts a git archive zip. Its comment names the
// commit, and every entry carries the commit time.
func githubZipToArchive(data []byte, hash string) ([]byte, time.Time, string, error) {
>>>>>>> origin/master
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, time.Time{}, "", err
	}
	builder := newArchiveBuilder()
	for _, entry := range reader.File {
		open, err := entry.Open()
		if err != nil {
			return nil, time.Time{}, "", err
		}
		err = builder.add(entry.Name, entry.Mode(), entry.Modified, open)
		open.Close()
		if err != nil {
			return nil, time.Time{}, "", err
		}
	}
	return builder.finish(reader.Comment, hash)
}

<<<<<<< HEAD
=======
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

>>>>>>> origin/master
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
