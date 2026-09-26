# Direct fetches from github.com

A module download tries each source in this order. The first one that works wins.

1. The module proxy (`GOPROXY`, `https://proxy.golang.org` by default).
2. `https://github.com/<owner>/<repo>/archive/<ref>.tar.gz`.
3. The same URL through `https://proxy.pazer.ai/?url=https://github.com/<owner>/<repo>/archive/<ref>.tar.gz`.
4. `https://github.com/<owner>/<repo>/archive/<ref>.zip`.
5. The same URL through `https://proxy.pazer.ai/?url=https://github.com/<owner>/<repo>/archive/<ref>.zip`.
6. A shallow `git fetch` of the commit, then the full history only when a command needs it.

A proxy request talks only to proxy.pazer.ai. The proxy must follow GitHub's redirect itself and return the archive. A redirect that it passes back is not followed. The next source is tried.

The archives are used only when the module proxy is skipped or misses. That happens under `GOPRIVATE`, `GONOPROXY`, `GOPROXY=direct`, or a 404/410 from the proxy. The code is `src/cmd/go/internal/modfetch/codehost/github.go`.

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

## No git until git is the only option

A github.com repository gets no local git repository at first. The archive, the refs and the kept files all live in plain files. The bare repository is made by the first git command, which runs only on the git fallback, or for `RecentTag` history.

The remote must be exactly `https://github.com/<owner>/<repo>` (with or without `.git`). Any other host, scheme, port or user name goes straight to git. github.com redirects each archive to `codeload.github.com`. `web.GetPinned` refuses a hop to any host other than those two. An API request may only reach its own host: api.github.com, github-state-mirror.pazer.io or proxy.pazer.ai.

A commit that no branch or tag names never comes from an archive. The git path has the same rule, so an unmerged pull request commit cannot pose as a pseudo-version.

The archive is used as GitHub serves it. It omits the commit of each submodule and applies `export-ignore` and `export-subst`, so such a module hashes differently than over git.

## What is kept

The archive is stored as GitHub served it, as `<vcs work dir>/github/<hash>.tar.gz` or `<hash>.zip`. Nothing converts it. `<hash>.time` holds the commit time and is written last, so it marks a complete archive. The archive is parsed in memory once per process. The module's files go from there straight into `$GOMODCACHE/<module>@<version>/`. Their `h1:` hash, the go.sum records, goes into `.ziphash`. No zip is made at any step, in the module cache or as a temporary file. `Stat` and `ReadFile` read the same parsed archive. As a result, a download needs no git objects at all. `RecentTag` still needs history. When a plausible tag exists, it runs the full git fetch.

`go get -x` prints each archive and API request, and why a source failed.
