# Org dependencies: pinned in go.mod, moved outside CI

An org module is a `github.com/wow-look-at-my/...` module. It follows the head of a branch, and `go.mod` records the pseudo-version of that head. The logic is in `src/cmd/go/internal/modload/orgbranch.go` and `orgsync.go`.

## Which head

- The main module's checked-out branch, when the dependency's repository has a branch of that name.
- A `// branch=NAME` comment on the require or replace line names another branch. The go-toolchain spellings `go-toolchain:branch=NAME` and `go-toolchain:auto-branch=NAME` are read too.
- The dependency's default branch otherwise. A detached HEAD, or a main module outside git, takes the default branch.

## CI builds the recorded version

A CI build is a go command with `GITHUB_ACTIONS=true`, no coding agent marker in the environment, and no coding agent among its ancestor processes. The markers are `CLAUDECODE`, `GROK_AGENT`, `CODEX_SANDBOX`, `CODEX_SANDBOX_NETWORK_DISABLED`, `GEMINI_CLI` and `OPENCODE`, when set to anything but `0`. The ancestor names start with `claude`, `grok`, `xai-grok-pager`, `codex`, `gemini` or `opencode`. The lists copy `github.com/wow-look-at-my/is-this-an-agent`. The code is `src/cmd/go/internal/orgmod/ci.go`. No other switch exists.

- A CI build uses the version on each org require and replace line as written. It looks up no branch head and writes nothing. So a commit that lands in the middle of a run reaches no job of it.
- A dependency's `go.mod` cannot move an org module there. Its org requirements take the version the main module records.
- A placeholder (`v0.0.0`, or `vN.0.0` for a `/vN` path) is an error that names the module. Run any go command outside CI to record the head.

## Every other run moves the pin

- Each go command resolves the head again and builds it. When it differs from the recorded version, the command writes the new pseudo-version to `go.mod`. It prints nothing about it.
- A placeholder is replaced the same way. The line keeps its `// branch=` and `go-toolchain:` comments.
- A command with no explicit `-mod` flag writes this, `go build` and `go run` included. An explicit `-mod=readonly` keeps its upstream meaning, and a head that moved fails as `updates to go.mod needed`.
- An org module has no `go.sum` line. Its commit is the integrity check.

## What an org module brings with it

A new commit of an org module can import a third-party module that the main module's `go.mod` and `go.sum` do not list yet. Upstream Go stops a build at that point. It reports `missing go.sum entry` or `updates to go.mod needed`. That tells the owner something they already know: the org vetted the new dependency when it merged it.

A command with no explicit `-mod` flag records that requirement in the same write that moves the org module.

- The go command reads the `go.mod` file of every org module in the requirement graph. The highest version any of them requires for a module is the declared version.
- A module at its declared version may take its checksum from the checksum database, the same way `go mod download` does. A hash that disagrees with `go.sum` or with the database still fails.
- The command writes `go.mod` and `go.sum` when the whole change moves org modules and adds or raises `// indirect` requirements to their declared versions.
- Any other change fails as `-mod=readonly` fails upstream: a new direct import, a module no org module declares, a changed `go` line, a lowered version.

## What stays upstream

- An explicit `-mod=readonly`, `-mod=vendor` or `-mod=mod` keeps its upstream meaning. So does a workspace, which resolves heads and writes no `go.mod`.
- Vendor mode resolves nothing. `vendor/modules.txt` records the placeholder, and the vendored packages take the version `go.mod` records, a placeholder included. That is how `src/cmd` builds, in CI as well.
- The files stay valid for stock Go. `go.mod` lists every module the build needs at a real version, and `go.sum` carries every third-party checksum.

The script tests `src/cmd/go/testdata/script/org_branch_head.txt`, `org_declared_sync.txt` and `org_ci_build.txt` cover each case. `org_ci_build.txt` skips where a coding agent is an ancestor of the test, because the go command never makes a CI build there.
