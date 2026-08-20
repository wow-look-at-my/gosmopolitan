# Toolchain Distribution

Every push whose build+test jobs are green publishes installable toolchain
tarballs to buildhost (pazer.build) as project `gosmopolitan`, for
**linux/amd64 and darwin/arm64**.

```bash
# Linux, x86-64
curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=linux&arch=amd64" | tar -xz
export PATH="$PWD/go/bin:$PATH"
go version   # go version go1.27.0cosmo.r<N> linux/amd64

# macOS, Apple Silicon
curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=darwin&arch=arm64" | tar -xz
export PATH="$PWD/go/bin:$PATH"
go version   # go version go1.27.0cosmo.r<N> darwin/arm64
```

The tarball extracts to `go/` (official distribution layout; GOROOT is
derived from the binary location, no need to set it).

## How the publish works

Three jobs in cosmo-ci.yml, because distpack packages what a HOST build
produced -- there is no cross-package shortcut, and
`GOOS=darwin GOARCH=arm64 ./make.bash -distpack` fails outright with
`distpack: stat bin/darwin_arm64/go: no such file or directory`:

- `publish-create` opens ONE buildhost release, so every platform lands in
  the same version.
- `publish-upload` is a matrix over ubuntu-latest/linux/amd64 and
  macos-latest/darwin/arm64. Each leg stamps VERSION, runs
  `make.bash -distpack` on its own runner (output
  `pkg/distpack/go<base>.r<run_number>.<goos>-<goarch>.tar.gz`, e.g.
  `go1.27.0cosmo.r75.linux-amd64.tar.gz`, ~64 MiB) and uploads it straight
  to buildhost.
- `publish-finish` publishes the release once every leg is in.

Nothing is handed between the jobs, so no GitHub Actions artifact storage
is involved. A failed leg means `publish-finish` never runs and the release
stays a DRAFT, which buildhost records as intent and never serves as latest
-- a half-uploaded release cannot be installed. Every step authenticates
with a GitHub Actions OIDC token (audience `https://pazer.build`) through
buildhost's own composite actions (`buildhost-create-release` /
`buildhost-upload-artifact` / `buildhost-publish-release`, referenced as
`wow-look-at-my/buildhost/.github/actions/<name>@master`).

One release holds many artifacts, keyed `{os}/{arch}`, so `os=`/`arch=`
select between them and neither platform can be served the other's bytes.

## The version stamp

The publish stamps VERSION with a unique per-release suffix
(`go<base>.r<run_number>`); the committed VERSION stays `go1.27.0cosmo`.
Every leg of one run stamps the SAME string, so the platforms of a release
cannot disagree about which toolchain they are.

The stamp exists because the fork identifies as a RELEASE Go version, so
cmd/go derives tool IDs (hence action IDs) from the version string alone.
Two releases sharing one string share a build-cache namespace, and the
org's shared GOCACHEPROG cache then links objects from different releases
into one binary. A monotonic suffix per publish keeps each release's cache
namespace disjoint.

Local source builds keep the static version and need no stamp: since
2026-07-20 tool IDs are content-derived (see CLAUDE.md's Fork Gotchas), so
a hand-rebuilt toolchain self-invalidates stale cache entries and the old
`go clean -cache`-after-`make.bash` rule is obsolete for local builds too.

## Consumer gotchas

- **`GOTOOLCHAIN=local` is no longer required.** The shipped `go.env` now
  defaults `GOTOOLCHAIN=local` (upstream ships `auto`, under which a consumer
  go.mod with a `go`/`toolchain` directive newer than this fork's version
  would silently download an official toolchain and lose cosmo). An explicit
  `GOTOOLCHAIN` env var or `go env -w` still overrides the default. A
  go.mod genuinely newer than the fork now fails loudly (`go.mod requires
  go >= X (running go 1.27)`) instead of silently switching. Note the fork
  self-identifies as the dev version `1.27` (its `go1.27.0cosmo` string does
  not parse as a release version), so directives up to `go 1.27` are
  satisfied but `go 1.27.0`+ are not. Releases published BEFORE this change
  still ship `GOTOOLCHAIN=auto` and need the env var.
- **Pin GOOS on host-side builds.** The fork defaults `GOOS=cosmo` (see Fork
  Gotchas); any host-run `go build`/`go install`/`go test` needs
  `GOOS=linux GOARCH=amd64` (or `darwin`/`arm64`).
- **Pinning**: `?branch=master` is a rolling latest that moves on every push
  to master (each branch gets its own `?branch=<name>` latest). Pin an
  immutable release with `?v=N` in place of the `branch` param; buildhost
  auto-increments N per publish, the publish job logs it, and
  `https://pazer.build/api/v1/projects/gosmopolitan/releases/latest` resolves
  the current one.
- **Other hosts build from source.** Windows, macOS Intel and linux/arm64
  have no published tarball; `cd src && ./make.bash` is the path there.
