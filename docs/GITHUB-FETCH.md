# Direct fetches from github.com

A github.com module download tries each source in this order. The first one that works wins.

1. `https://github.com/<owner>/<repo>/archive/<ref>.tar.gz`.
2. The same URL through `https://proxy.pazer.ai/?url=https://github.com/<owner>/<repo>/archive/<ref>.tar.gz`.
3. `https://github.com/<owner>/<repo>/archive/<ref>.zip`.
4. The same URL through `https://proxy.pazer.ai/?url=https://github.com/<owner>/<repo>/archive/<ref>.zip`.
5. For a private repository: the archive github-state-mirror signs. See "Private repositories" below.
6. A shallow `git fetch` of the commit, then the full history only when a command needs it.

The go command never asks `https://proxy.golang.org`. `DefaultGOPROXY` is `direct`, and `newProxyRepo` refuses that host from any source, a go-import `mod` tag included.

A proxy request talks only to proxy.pazer.ai. The proxy must follow GitHub's redirect itself and return the archive. A redirect that it passes back is not followed. The next source is tried.

These are the `github` entry that `proxyList` puts ahead of `direct` (`src/cmd/go/internal/modfetch/proxy.go`). A failure there is final, and `direct` is not tried a second time. A module outside github.com skips the entry and goes direct to its origin. The go-import meta tag names the repository, and a repository on github.com takes the archive route again. A path under `GOPRIVATE` or `GONOPROXY` goes direct too. The archive code is `src/cmd/go/internal/modfetch/codehost/github.go`.

An archive can hash differently from the module zip: `export-ignore` and `export-subst` change it. When go.sum records an `h1:` sum the archive does not match. The commit is fetched with no history and zipped by `git archive` with those attributes off (`codehost.WithGitOnly`). The log names the archive as the failed source.

## Which URLs

| Ref | Archive path |
|---|---|
| `refs/tags/v1.2.3` | `archive/refs/tags/v1.2.3.tar.gz` |
| `refs/heads/master` | `archive/refs/heads/master.tar.gz` |
| `HEAD` | `archive/<commit hash>.tar.gz` |

## Tags, branches and commits without git

A version tag such as `v1.2.3` needs no ref list. Its archive is fetched first. The commit comes out of the archive: the tar.gz pax `comment` record, or the zip comment.

Everything else needs the ref list. It comes over plain HTTP, from the first source that answers:

1. `https://github.com/<owner>/<repo>.git/info/refs?service=git-upload-pack`. This is the advertisement git itself reads first: every branch and tag at its commit, annotated tags peeled, and `HEAD`. One request, no rate limit.
2. The same URL through proxy.pazer.ai.
3. The REST API on github-state-mirror.pazer.io. The mirror answers only a request with a GitHub token.
4. The REST API on api.github.com, then through proxy.pazer.ai. Without a token it allows few requests an hour.
5. `git ls-remote`.

The REST steps read `/repos/<owner>/<repo>/tags` and `/branches` page by page, and `/repos/<owner>/<repo>` for `default_branch`, which is `HEAD`.

A request through proxy.pazer.ai carries the GOAUTH credential of the github.com URL it wraps, and the proxy forwards it. A request to github-state-mirror carries the credential for api.github.com. A netrc entry for api.github.com therefore covers the mirror, the API and the proxied API.

## Private repositories

github.com answers nothing about a private repository without a credential. GOAUTH usually has none for it. The direct `info/refs` advertisement is retried once with the credential git's own helpers hold for github.com (`git credential fill`), presented as HTTP basic authentication. On a machine whose git has one, a private repository's ref list therefore costs one GET, not a `git ls-remote`. The retry runs only when the first advertisement fails and no other credential was configured. With no git credential the ref list falls to the routes below. github-state-mirror is the route that needs no git. It is asked first with the GOAUTH credential for api.github.com. Then it is asked once with each token in `GH_ENTERPRISE_TOKEN`, `GITHUB_ENTERPRISE_TOKEN`, `GITHUB_TOKEN` and `GH_TOKEN`, in that order. After those, each other environment value that starts like a GitHub token (`ghp_`, `github_pat_`, `gho_`, `ghu_`, `ghs_`) is tried, in the order of the variable names. A web session needs that search: its real tokens sit in variables with their own names, and `GH_TOKEN` holds a proxy placeholder. A token goes to the mirror only. The token the mirror last accepted is tried first after that.

- The ref list comes from the mirror's REST API, as above.
- The archive comes from `/repos/<owner>/<repo>/tarball/<commit>`, then `zipball`. The mirror answers with a redirect to a codeload.github.com URL that carries its own short-lived token. That redirect is not followed. The signed URL is fetched directly, then through proxy.pazer.ai, with no credential. An error prints `xxxxx` in place of the token.

The route reads `tar.gz archive via github-state-mirror.pazer.io`, with `and proxy.pazer.ai` when the proxy carried it.

## No git until git is the only option

A github.com repository gets no local git repository at first. The archive, the refs and the kept files all live in plain files. The bare repository is made by the first git command, which runs only on the git fallback, or for `RecentTag` history.

The remote must be exactly `https://github.com/<owner>/<repo>` (with or without `.git`). Any other host, scheme, port or user name goes straight to git. github.com redirects each archive to `codeload.github.com`. `web.GetPinned` refuses a hop to any host other than those two. An API request may only reach its own host: api.github.com, github-state-mirror.pazer.io or proxy.pazer.ai. A signed archive URL must be on codeload.github.com.

A commit that no branch or tag names never comes from an archive. The git path has the same rule, so an unmerged pull request commit cannot pose as a pseudo-version.

The archive is used as GitHub serves it. It omits the commit of each submodule and applies `export-ignore` and `export-subst`, so such a module hashes differently than over git.

## What is kept

The archive is stored as GitHub served it, as `<vcs work dir>/github/<hash>.tar.gz` or `<hash>.zip`. `<hash>.time` holds the commit time and is written last, so it marks a complete archive. The archive is parsed in memory once per process.

The module's files go from the parsed archive straight into `$GOMODCACHE/<module>@<version>/`. No zip is made, in the module cache or as a temporary file. `<version>.archive` marks such a module, and only such a module may lack its zip. `Stat` and `ReadFile` read the same parsed archive. As a result, a download needs no git objects. `RecentTag` still runs the full git fetch when a plausible tag exists.

The sum is `git:<commit>`, for the module and for its go.mod. GitHub is trusted to serve that commit, so nothing hashes the files and the checksum database is not asked. The git fallback records the same sum. A go.sum that already has an `h1:` line gets the `h1:` sum computed and checked. The sum goes into `.ziphash`. A cached go.mod keeps its commit in `<version>.commit`.

`go get -x` prints each archive and API request, and why a source failed.

Each fetched module gets one line when it is complete, for every route:

```
go: downloading github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99: tar.gz archive, 1.2 MB in 0.40s (3.0 MB/s), 0.62s total
```

The route is the module proxy, a `tar.gz` or `zip` archive (direct or `via proxy.pazer.ai`), or `git`. When an earlier source failed, `because` follows the route and names each one with its HTTP status or error, as in `tar.gz archive via proxy.pazer.ai, because github.com tar.gz: 403 Forbidden`. The size is the bytes of every attempt that received data. The git size is the growth of the object store. The total adds the part of a transfer that `Stat` ran before the download began. A module already in the module cache prints nothing. The code is `cmd/go/internal/modfetch/fetchlog.go`.
