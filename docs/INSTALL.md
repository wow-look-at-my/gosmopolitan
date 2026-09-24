# Toolchain Distribution

Every push whose build+test jobs are green publishes installable toolchain tarballs to buildhost (pazer.build) as project `gosmopolitan`, for **linux/amd64, darwin/arm64 and windows/amd64**.

```bash
curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=linux&arch=amd64" | tar -xz
export PATH="$PWD/go/bin:$PATH"
go version

# macOS, Apple Silicon
curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=darwin&arch=arm64" | tar -xz
export PATH="$PWD/go/bin:$PATH"
go version

curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=windows&arch=amd64" -o go.tar.gz
tar -xzf go.tar.gz
go\bin\go version
```

The tarball extracts to `go/` (official distribution layout. GOROOT is derived from the binary location, no need to set it).

Every slot uploads a `.tar.gz`, windows included. A GOROOT is a directory tree, and buildhost stores one blob per os/arch and repackages it at download time. The archive IS the. Upstream's windows-only `.zip` can be a second copy nothing fetches, so distpack no longer writes one. The `.exe` suffix inside is what differs: windows carries `go/bin/go.exe`.

## How the publish works

Packaging and uploading are separate, because they have different requirements.

Packaging runs in the build leg, on the host that produced the toolchain. `dist stamp` relinks `bin/go` and then execs it to check the version took, and distpack ships that host's own `pkg/tool/$GOOS_$GOARCH`. Neither step has a cross-host form: `GOOS=darwin GOARCH=arm64 ./bin/go tool distpack` on a linux tree fails with `distpack: stat bin/darwin_arm64/go: no such file or directory`. The leg hands its `go<VERSION>.<goos>-<goarch>.tar.gz` over as `distpack-<goos>-<goarch>`, after it hands the unstamped toolchain over, so the suites take the tree the build left.

Uploading is generic work, so every publish job runs on ubuntu-latest:

- `publish-create` opens ONE buildhost release, so every platform lands in the same version.
- `publish-upload` is a matrix over linux/amd64 and darwin/arm64. Each leg takes its `distpack-<goos>-<goarch>` archive and uploads it to buildhost. No mac or windows runner is acquired here.
- `publish-finish` publishes the release once every leg is in.

The archives travel through the org cache, so no GitHub Actions artifact storage is involved. A failed leg means `publish-finish` never runs and the release stays a DRAFT, which buildhost records as intent and never serves as latest -- a. Every step authenticates with a GitHub Actions OIDC token (audience `https://pazer.build`) through buildhost's own composite actions (`buildhost-create-release` / `buildhost-upload-artifact` / `buildhost-publish-release`, referenced as `wow-look-at-my/buildhost/.github/actions/<name>@master`).

One release holds many artifacts, keyed `{os}/{arch}`, so `os=`/`arch=` select between them and neither platform can be served the other's bytes.

## The version an installed toolchain reports

Every release reports the committed VERSION, `go1.27.0-cosmo`. The tarball is named for it. The buildhost version is what tells releases apart. `?v=N` selects it, and the publish jobs never write it into the tree.

Nothing needs a per-release Go version string. A fork tool prints its own `buildID=` under `-V=full`. So cmd/go keys the build cache on the tool's content, not on the version it claims. A toolchain built from another source gets another tool ID, whatever its VERSION says. One built from the same source is the same toolchain.

Local source builds keep the static version and need no stamp: since 2026-07-20 tool IDs are content-derived (see CLAUDE.md's Fork Gotchas). A hand-rebuilt toolchain self-invalidates stale.

## Consumer gotchas

<<<<<<< HEAD
- **`GOTOOLCHAIN` defaults to `local`.** The shipped `go.env` sets it. Upstream ships `auto`, under which a consumer go.mod naming a newer Go silently downloads an official toolchain and loses cosmo. A `GOTOOLCHAIN` environment variable or `go env -w` still overrides the default. A go.mod genuinely newer than the fork fails with `go.mod requires go >= X`. The fork self-identifies as the dev version `1.27`, because `go1.27.0cosmo` does not parse as a release version, so a directive up to `go 1.27` is satisfied and `go 1.27.0` is not.
=======
- **`GOBIN` and `GOTOOLCHAIN` are removed.** Neither is a go command variable here. `go env` reports neither. `go env -w` refuses both with `unknown go command variable`. The go command drops both from its own environment and from every process it starts. So a value in the environment reaches nothing. A `go/env` file written by another toolchain reaches nothing either. `go install` therefore always lands in `$GOPATH/bin`, or in `$GOROOT/bin` for a command in GOROOT. This fork always runs itself. A consumer go.mod that names a newer `go` or `toolchain` fails with `go.mod requires go >= X`. Upstream downloads an official toolchain there and loses cosmo. Depth: `RemovedEnv` in `src/cmd/go/internal/cfg/cfg.go`.
- **The suffix comes after a dash. That makes the fork Go 1.27.0.** `gover.Parse` rejects a patch release with a trailing word. So the older `go1.27.0cosmo` spelling was no valid version anywhere. `go/version.IsValid(runtime.Version())` was false. cmd/go fell back to the development version `1.27` and refused a go.mod that requires `go 1.27.0` or newer. `go1.27.0-cosmo` is the custom-toolchain syntax every parser already strips. The fork now satisfies those directives and identifies as the release 1.27.0. The release stamp `go1.27.0-cosmo.r<N>` parses the same way. This is what lets gopls build against the fork: its own go.mod requires `go 1.27.0`. Depth: docs/GOPLS.md.
- **The fork self-identifies as the dev version `1.27`**, because `go1.27.0cosmo` does not parse as a release version. A directive up to `go 1.27` is therefore satisfied. `go 1.27.0` is not.
>>>>>>> origin/master
- **That unparseable version has a price, and it reaches shipped binaries.** `gover.Parse` rejects a patch release with a trailing word, so `go/version.IsValid(runtime.Version())` is FALSE. Spelling VERSION `go1.27.0-cosmo` can fix it, because `go/version` cuts at the first `-`. It can also make the fork parse as the RELEASE 1.27.0 and start satisfying the `go 1.27.0`+ directives the paragraph above says it refuses, which. Both spellings are defensible. Pick one deliberately rather than as a side effect.
- **Pin GOOS on host-side builds.** The fork defaults `GOOS=cosmo` (see Fork Gotchas). Any host-run `go build`/`go install`/`go test` needs `GOOS=linux GOARCH=amd64` (or `darwin`/`arm64`).
- **Pinning**: `?branch=master` is a rolling latest that moves on every push to master (each branch gets its own `?branch=<name>` latest). Pin an immutable release with `?v=N` in place of the `branch` param. Buildhost auto-increments N per publish, the publish job logs it, and `https://pazer.build/api/v1/projects/gosmopolitan/releases/latest` resolves the current one.
- **Other hosts build from source.** macOS Intel and linux/arm64 have no published tarball.`cd src && ./make.bash` is the path there.
