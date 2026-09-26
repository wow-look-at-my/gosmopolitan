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

| Ref from `git ls-remote` | Archive path |
|---|---|
| `refs/tags/v1.2.3` | `archive/refs/tags/v1.2.3.tar.gz` |
| `refs/heads/master` | `archive/refs/heads/master.tar.gz` |
| `HEAD` | `archive/<commit hash>.tar.gz` |

The remote must be exactly `https://github.com/<owner>/<repo>` (with or without `.git`). Any other host, scheme, port or user name goes straight to git. github.com redirects each archive to `codeload.github.com`. `web.GetPinned` refuses a hop to any host other than those two, or other than `proxy.pazer.ai` for a proxy request.

A commit that no branch or tag names never comes from an archive. The git path has the same rule, so an unmerged pull request commit cannot pose as a pseudo-version.

The archive is used as GitHub serves it. It omits the commit of each submodule and applies `export-ignore` and `export-subst`, so such a module hashes differently than over git.

## What is kept

The converted archive is stored as `<vcs work dir>/github/<hash>.zip`. Its comment holds the hash and the commit time. `Stat`, `ReadFile` and `ReadZip` all read from it, so a download needs no git objects at all. `RecentTag` still needs history. When a plausible tag exists, it runs the full git fetch.

`go get -x` prints each archive request and why a source failed.
