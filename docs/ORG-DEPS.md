# What an org module brings with it

An org module is a `github.com/wow-look-at-my/...` module. It carries a placeholder version, and the go command resolves it to a branch head on each run (`src/cmd/go/internal/modload/orgbranch.go`). Its next commit can therefore import a third-party module that the main module's `go.mod` and `go.sum` do not list yet.

Upstream Go stops a build at that point. It reports `missing go.sum entry` or `updates to go.mod needed`. That tells the owner something they already know: the org vetted the new dependency when it merged it.

## What the fork does

A command with no explicit `-mod` flag records that requirement and continues. The logic is in `src/cmd/go/internal/modload/orgsync.go`.

- The go command reads the `go.mod` file of every org module in the requirement graph. The highest version any of them requires for a module is the declared version.
- A module at its declared version may take its checksum from the checksum database, the same way `go mod download` does. A hash that disagrees with `go.sum` or with the database still fails.
- The command writes `go.mod` and `go.sum` when the whole change adds or raises `// indirect` requirements to their declared versions. It prints nothing about it.
- Any other change fails as `-mod=readonly` fails upstream: a new direct import, a module no org module declares, a changed `go` line, a lowered version.
- `go mod tidy` already records these requirements. The build path now agrees with it. As a result, a run can no longer fail when an org module moves between two of its commands.

## What stays upstream

- An explicit `-mod=readonly`, `-mod=vendor` or `-mod=mod` keeps its upstream meaning. So does a workspace.
- The files stay valid for stock Go. `go.mod` lists every module the build needs, and `go.sum` carries every checksum, so a stock toolchain builds the same tree.
- Modules that no org module declares follow the upstream rules exactly.

The script test `src/cmd/go/testdata/script/org_declared_sync.txt` covers each case.
