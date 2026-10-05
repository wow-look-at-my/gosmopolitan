// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

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
	"iter"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
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

// githubRemote names the GitHub repository of a git remote.
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
// may touch.
type archiveSource struct {
	url           string
	ext           string
	allowHost     func(string) bool
	credentialFor string
	// bearer is the whole credential when it is set. GOAUTH is not asked.
	bearer string
	// via names the route of a source that github-state-mirror signed.
	via string
	// secret is the signing token in url. Errors print a mask in its place.
	secret string
}

func (source archiveSource) pinOptions() web.PinOptions {
	return web.PinOptions{AllowHost: source.allowHost, CredentialURL: source.credentialFor, Bearer: source.bearer}
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

// route names source in the line that reports a download.
func (source archiveSource) route() string {
	route := archiveRoute(source.ext)
	if source.via != "" {
		route += " via " + source.via
	} else if source.credentialFor != "" {
		route += " via " + proxyHost
	}
	return route
}

// name is the host and the format of source, as "github.com tar.gz".
func (source archiveSource) name() string {
	host := "github.com"
	if source.via != "" {
		host = source.via
	} else if source.credentialFor != "" {
		host = proxyHost
	}
	return host + " " + strings.TrimPrefix(source.ext, ".")
}

// archiveRoute names an archive by its extension, as "tar.gz archive".
func archiveRoute(ext string) string {
	return strings.TrimPrefix(ext, ".") + " archive"
}

// keptArchiveRoute names the kept archive of hash that no fetch in this
// process wrote.
func (r *gitRepo) keptArchiveRoute(hash string) string {
	for _, ext := range []string{".tar.gz", ".zip"} {
		if _, err := os.Stat(r.githubArchivePath(hash, ext)); err == nil {
			return archiveRoute(ext)
		}
	}
	return "archive"
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

// maxAPIResponse bounds one page of an api.github.com list.
const maxAPIResponse = 16 << 20

// gsmHost is github-state-mirror, a cache of the GitHub API that the owner of this fork runs.
const gsmHost = "github-state-mirror.pazer.io"

func isGSMHost(host string) bool { return host == gsmHost }

// githubTokenVars name the environment variables that can hold a GitHub token.
var githubTokenVars = []string{"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"}

var (
	gsmAcceptedMu sync.Mutex
	gsmAccepted   string
)

// githubTokenPrefixes start every token GitHub issues.
var githubTokenPrefixes = []string{"ghp_", "github_pat_", "gho_", "ghu_", "ghs_"}

// gsmBearers returns the token the mirror last accepted, each token that
// githubTokenVars hold, then each other environment value that starts like a
// GitHub token, in the order of the variable names.
func gsmBearers() []string {
	gsmAcceptedMu.Lock()
	accepted := gsmAccepted
	gsmAcceptedMu.Unlock()
	var tokens []string
	add := func(token string) {
		if token != "" && !slices.Contains(tokens, token) {
			tokens = append(tokens, token)
		}
	}
	add(accepted)
	for _, name := range githubTokenVars {
		add(os.Getenv(name))
	}
	environ := os.Environ()
	slices.Sort(environ)
	for _, entry := range environ {
		_, value, _ := strings.Cut(entry, "=")
		if slices.ContainsFunc(githubTokenPrefixes, func(prefix string) bool { return strings.HasPrefix(value, prefix) }) {
			add(value)
		}
	}
	return tokens
}

func gsmAccept(token string) {
	if token == "" {
		return
	}
	gsmAcceptedMu.Lock()
	gsmAccepted = token
	gsmAcceptedMu.Unlock()
}

// gsmSources returns the requests that ask the mirror for route: one with the
// GOAUTH credential for api.github.com, then one for each token of gsmBearers.
func gsmSources(route string) []archiveSource {
	direct := "https://api.github.com" + route
	sources := []archiveSource{{url: "https://" + gsmHost + route, allowHost: isGSMHost, credentialFor: direct}}
	for _, token := range gsmBearers() {
		sources = append(sources, archiveSource{url: "https://" + gsmHost + route, allowHost: isGSMHost, bearer: token})
	}
	return sources
}

// githubRefs returns what ls-remote would list, over plain HTTP. The first
// source that answers wins: the info/refs advertisement from github.com,
// direct and then through the proxy. This also covers then the REST API, from
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
	sources := append(gsmSources(route),
		archiveSource{url: direct, allowHost: isAPIHost},
		viaProxy(direct, ""),
	)
	var errs []error
	for _, source := range sources {
		u, err := url.Parse(source.url)
		if err != nil {
			return err
		}
		resp, err := web.GetPinnedWith(u, source.pinOptions())
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
			gsmAccept(source.bearer)
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

// githubArchivePath is where the archive of hash is kept, byte for byte as
// GitHub served it.
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
}

// statGitHub describes hash from an archive of ref, from the first source
// in archiveSources that works. The archive is kept on disk, so ReadFile and
// ReadZip never have to fetch the commit with git. rec gets the route and the
// transfers. It requires r.mu.
func (r *gitRepo) statGitHub(ctx context.Context, version, ref, hash string, rec *Fetch) (*RevInfo, error) {
	if r.github == nil || r.sha256Hashes || len(hash) != 40 {
		return nil, errors.New("not a github.com repository with SHA-1 hashes")
	}
	if when, err := r.githubArchiveTime(hash); err == nil {
		return r.githubRevInfo(ctx, version, hash, when, nil), nil
	}

	var errs []error
	for source := range r.archiveCandidates(ctx, ref, hash, hash, rec, &errs) {
		raw, entries, when, _, err := r.downloadGitHub(ctx, source, hash, rec)
		if err != nil {
			errs = append(errs, err)
			rec.AddFailure(source.name(), err)
			continue
		}
		if err := r.keepGitHub(hash, source.ext, raw, entries, when); err != nil {
			return nil, err
		}
		rec.SetRoute(source.route())
		return r.githubRevInfo(ctx, version, hash, when, nil), nil
	}
	return nil, errors.Join(errs...)
}

// archiveCandidates yields the sources of archiveSources. Then, for a
// repository that needs a credential, it yields the archive of rev that
// github-state-mirror signs. The mirror is asked only after every source
// before it failed. A failure to get a signed URL goes to rec and errs.
func (r *gitRepo) archiveCandidates(ctx context.Context, ref, hash, rev string, rec *Fetch, errs *[]error) iter.Seq[archiveSource] {
	return func(yield func(archiveSource) bool) {
		for _, source := range r.github.archiveSources(ref, hash) {
			if !yield(source) {
				return
			}
		}
		for _, ext := range []string{".tar.gz", ".zip"} {
			signed, err := r.gsmArchiveURL(ctx, ext, rev)
			if err != nil {
				*errs = append(*errs, err)
				rec.AddFailure(gsmHost+" "+strings.TrimPrefix(ext, "."), err)
				continue
			}
			secret := signed.Query().Get("token")
			direct := archiveSource{url: signed.String(), ext: ext, allowHost: isCodeloadHost, via: gsmHost, secret: secret}
			proxied := archiveSource{
				url:       "https://" + proxyHost + "/?url=" + url.QueryEscape(signed.String()),
				ext:       ext,
				allowHost: isProxyHost,
				via:       gsmHost + " and " + proxyHost,
				secret:    secret,
			}
			if !yield(direct) || !yield(proxied) {
				return
			}
		}
	}
}

func isCodeloadHost(host string) bool { return host == "codeload.github.com" }

// gsmArchiveURL asks github-state-mirror where the archive of rev is. The
// answer is a codeload.github.com URL that carries its own short-lived token,
// so the download that follows needs no credential.
func (r *gitRepo) gsmArchiveURL(ctx context.Context, ext, rev string) (*url.URL, error) {
	kind := "tarball"
	if ext == ".zip" {
		kind = "zipball"
	}
	segments := strings.Split(rev, "/")
	for idx, seg := range segments {
		segments[idx] = url.PathEscape(seg)
	}
	route := "/repos/" + r.github.owner + "/" + r.github.name + "/" + kind + "/" + strings.Join(segments, "/")
	var errs []error
	for _, source := range gsmSources(route) {
		u, err := url.Parse(source.url)
		if err != nil {
			return nil, err
		}
		opts := source.pinOptions()
		opts.NoRedirect = true
		resp, err := web.GetPinnedWith(u, opts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if resp.StatusCode >= 400 {
			errs = append(errs, resp.Err())
			resp.Body.Close()
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 300 {
			errs = append(errs, fmt.Errorf("%s: %s is not a redirect", u.Redacted(), resp.Status))
			continue
		}
		var location string
		if values := resp.Header["Location"]; len(values) > 0 {
			location = values[0]
		}
		signed, err := url.Parse(location)
		if err != nil || signed.Scheme != "https" || !isCodeloadHost(signed.Hostname()) {
			errs = append(errs, fmt.Errorf("%s: redirect to %q is not a codeload.github.com URL", u.Redacted(), signed.Redacted()))
			continue
		}
		gsmAccept(source.bearer)
		return signed, nil
	}
	err := errors.Join(errs...)
	if xLog, ok := cfg.BuildXWriter(ctx); ok {
		fmt.Fprintf(xLog, "# github-state-mirror archive: %v\n", err)
	}
	return nil, err
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
// commit, so ls-remote does not run. rec gets the route and the transfers.
// It requires r.mu.
func (r *gitRepo) statGitHubTag(ctx context.Context, tag string, rec *Fetch) (*RevInfo, error) {
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
	for source := range r.archiveCandidates(ctx, "refs/tags/"+tag, "", tag, rec, &errs) {
		raw, entries, when, hash, err := r.downloadGitHub(ctx, source, "", rec)
		if err != nil {
			errs = append(errs, err)
			rec.AddFailure(source.name(), err)
			continue
		}
		if err := r.keepGitHub(hash, source.ext, raw, entries, when); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(r.githubTagPath(tag), []byte(hash+"\n")); err != nil {
			return nil, err
		}
		rec.SetRoute(source.route())
		return r.githubTagInfo(ctx, tag, hash, when), nil
	}
	return nil, errors.Join(errs...)
}

func (r *gitRepo) githubTagInfo(ctx context.Context, tag, hash string, when time.Time) *RevInfo {
	info := r.githubRevInfo(ctx, tag, hash, when, []string{tag})
	info.Origin.Ref = "refs/tags/" + tag
	return info
}

// downloadGitHub fetches one archive and parses it. It returns the bytes as
// served, the entries, the commit time and the commit. hash is the commit when
// the archive does not name one. rec gets the transfer, when it received data.
func (r *gitRepo) downloadGitHub(ctx context.Context, source archiveSource, hash string, rec *Fetch) ([]byte, []archiveEntry, time.Time, string, error) {
	raw, entries, when, commit, err := r.downloadGitHubOnce(ctx, source, hash, rec)
	if err != nil && source.secret != "" {
		err = errors.New(strings.ReplaceAll(err.Error(), source.secret, "xxxxx"))
	}
	if err != nil {
		if xLog, ok := cfg.BuildXWriter(ctx); ok {
			fmt.Fprintf(xLog, "# github archive: %v\n", err)
		}
	}
	return raw, entries, when, commit, err
}

func (r *gitRepo) downloadGitHubOnce(ctx context.Context, source archiveSource, hash string, rec *Fetch) ([]byte, []archiveEntry, time.Time, string, error) {
	u, err := url.Parse(source.url)
	if err != nil {
		return nil, nil, time.Time{}, "", err
	}
	start := time.Now()
	resp, err := web.GetPinnedWith(u, source.pinOptions())
	if err != nil {
		return nil, nil, time.Time{}, "", err
	}
	defer resp.Body.Close()
	if err := resp.Err(); err != nil {
		return nil, nil, time.Time{}, "", err
	}
	body := &io.LimitedReader{R: resp.Body, N: MaxZipFile + 1}
	raw, err := io.ReadAll(body)
	rec.AddTransfer(start, int64(len(raw)), time.Since(start))
	if err == nil && body.N <= 0 {
		err = errors.New("archive too large")
	}
	var entries []archiveEntry
	var when time.Time
	var commit string
	if err == nil {
		entries, when, commit, err = parseArchive(raw, source.ext, hash)
	}
	if err != nil {
		return nil, nil, time.Time{}, "", fmt.Errorf("%s: %w", u.Redacted(), err)
	}
	return raw, entries, when, commit, nil
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

// A FileReader serves a revision's files directly. The files go straight into
// the module cache, and no zip is made of them.
type FileReader interface {
	ReadFiles(ctx context.Context, rev, subdir string) ([]ModuleFile, error)
}

// An archiveEntry is one file or symlink of an archive, named without the
// archive's top directory.
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
	}
	return nil, fs.ErrNotExist
}

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

// An archiveBuilder collects the entries of a GitHub archive: files and
// symlinks only, each named without the top-level directory.
type archiveBuilder struct {
	top     string
	when    time.Time
	entries []archiveEntry
}

func newArchiveBuilder() *archiveBuilder {
	return &archiveBuilder{}
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
	b.entries = append(b.entries, archiveEntry{name: rest, mode: mode, data: data})
	return nil
}

// finish returns the entries, the commit time and the commit. embedded is the
// commit the archive names. hash stands in when the archive names none.
func (b *archiveBuilder) finish(embedded, hash string) ([]archiveEntry, time.Time, string, error) {
	commit := hash
	if len(embedded) == 40 && AllHex(embedded) {
		commit = embedded
	}
	if commit == "" {
		return nil, time.Time{}, "", errors.New("archive names no commit")
	}
	if len(b.entries) == 0 {
		return nil, time.Time{}, "", errors.New("archive holds no files")
	}
	return b.entries, b.when, commit, nil
}

// parseZipArchive reads a git archive zip. Its comment names the commit,
// and every entry carries the commit time.
func parseZipArchive(data []byte, hash string) ([]archiveEntry, time.Time, string, error) {
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
