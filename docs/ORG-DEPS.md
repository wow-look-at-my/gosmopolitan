# Org dependencies: branch heads, locked per CI run

An org module is a `github.com/wow-look-at-my/...` module. It has no version of its own. `go.mod` records the placeholder, `v0.0.0` or `vN.0.0` for a `/vN` path, and the go command resolves it in memory to the head of a branch. The logic is in `src/cmd/go/internal/modload/orgbranch.go` and `orgsync.go`.

## Which head

- The main module's checked-out branch, when the dependency's repository has a branch of that name.
- A `// branch=NAME` comment on the require or replace line names another branch. The go-toolchain spellings `go-toolchain:branch=NAME` and `go-toolchain:auto-branch=NAME` are read too.
- The dependency's default branch otherwise. A detached HEAD, or a main module outside git, takes the default branch.

## go.mod keeps the placeholder

- Each go command resolves the head again and builds it. The version lives in the build list, the module cache and `go version -m`, not in `go.mod`.
- A go command keeps the heads it resolved under `$GOMODCACHE/cache/org/heads/<pid>-<start time>/` while it runs, and removes them when it exits. A go command that it starts, through a `go generate` directive, a `go run` program or a test, builds those heads and resolves none of them again. So one `go generate` resolves each head once. The code is `src/cmd/go/internal/orgmod/procheads.go`, and `org_heads_passed.txt` covers it.
- No variable and no flag carries a head. A child walks its own ancestors in `/proc` and reads only the record of a live ancestor. The start time tells a recycled pid apart. A forged record therefore means a write into the module cache, which can already change any module's source. A host without `/proc` passes nothing, and each go command resolves its heads itself.
- `go mod tidy`, `go get` and every build write the placeholder for an org module. A tidy go.mod stays byte for byte identical when an org dependency moves.
- The token on an org require or replace line is not read. A go.mod file that records some other version builds the same way, and the next write puts the placeholder back.
- An org module has no `go.sum` line. Its commit is the integrity check.

## A CI run locks each head

A CI build is a go command with `GITHUB_ACTIONS=true`, no coding agent marker in the environment, and no coding agent among its ancestor processes. The markers are `CLAUDECODE`, `GROK_AGENT`, `CODEX_SANDBOX`, `CODEX_SANDBOX_NETWORK_DISABLED`, `GEMINI_CLI` and `OPENCODE`, when set to anything but `0`. The ancestor names start with `claude`, `grok`, `xai-grok-pager`, `codex`, `gemini` or `opencode`. The lists copy `github.com/wow-look-at-my/is-this-an-agent`. The code is `src/cmd/go/internal/orgmod/ci.go`. No other switch exists.

A CI build follows the same heads as any other build, and writes the same placeholder.

- A run is one attempt of one workflow run: `GITHUB_REPOSITORY`, `GITHUB_RUN_ID` and `GITHUB_RUN_ATTEMPT`. A lock is an org module on a branch, inside a run.
- `GOSMOPOLITAN_RUN` names the run as `owner/repo/run-id/attempt` when `GITHUB_RUN_ID` or `GITHUB_RUN_ATTEMPT` is blank. A caller that blanks those for its children sets it, so a test binary never reads a per-run value and its result stays cacheable. A malformed value is an error.
- The first go command of the run that resolves a lock claims the head it found. Every later go command of the run, in any job on any machine, builds the claimed version instead of the head. A commit that lands in the middle of a run therefore reaches no job of it. A re-run is a new attempt, so it claims the heads again.
- The claim is a create-if-absent. Of racing claims one wins, and every build takes the winner's version.
- Inside one go command the resolved version stays cached, so a command asks the store once per lock.
- A lock never changes inside its run. As a result, the machine keeps a copy of each lock it reads, under `$GOMODCACHE/cache/org/run-locks/`, one directory per store. A later go command of the job reads the copy and sends no request, so the job asks the store once per lock. Only the command that asked prints the `building` line. The key names the run and the attempt, so a copy never reaches another run.
- A store that refuses, or a CI build that names no run, fails the command. The error names the store and the module. Nothing stands in for the lock.
- A dropped connection, a 5xx or a 429 is the path to the store, not its answer. The command asks again on a fixed cadence until the store answers, and names each failure on stderr.
- A build that names no run reads and writes no store, and resolves every head for itself.

The code is `src/cmd/go/internal/orgmod/runlock.go`.

## A local build can name a run too

A build driver that runs many go commands in one build pays to resolve each org module head once per command. Naming the run in `GOSMOPOLITAN_RUN` locks the heads for that build the way a CI run does. The run is one build, not one machine: the driver picks a name unique to it, and every go command it starts inherits it.

- A build locks a run when it is a CI job or when `GOSMOPOLITAN_RUN` names one. A build that names neither is unchanged.
- With no store named, a local run's locks live in a directory of their own under the user cache (`gosmopolitan/run-locks/<run>`). One build never reads another's locks, and no job authenticates to a server.
- `GOSMOPOLITAN_RUN_LOCK_STORE` names the store as it does in CI.
- A changed head is picked up by the next run: a build that names a new run resolves the heads again.
- `cmd/go/testdata/script/org_local_run.txt` covers the reuse and the new run. `org_ci_build.txt` covers the CI run.

### The store

The store is the buildhost server at `https://pazer.build`. `GOSMOPOLITAN_RUN_LOCK_STORE` names another store as a URL. A `file:` URL names a directory that every job must share. The script test uses one.

- The job authenticates with its GitHub Actions OIDC token, whose audience is the store URL. The workflow needs `permissions: id-token: write` and no secret.
- `GET /api/v1/run-locks` looks a lock up by `repository`, `run_id`, `run_attempt` and `name` (`module@branch`). The answer is `{"found": bool, "value": version}`.
- `POST /api/v1/run-locks` takes the same fields and a `value` as JSON. It stores the value unless the lock exists, and answers with the value it holds.
- buildhost keys each lock by the repository, run and attempt the token names, and refuses a request for another run. It forgets a lock once no run can still use it.
- A directory store writes a temporary file and links it into place. A link fails when the name exists, which makes the claim atomic.

## What an org module brings with it

A new commit of an org module can import a third-party module that the main module's `go.mod` and `go.sum` do not list yet. Upstream Go stops a build at that point. It reports `missing go.sum entry` or `updates to go.mod needed`. That tells the owner something they already know: the org vetted the new dependency when it merged it.

A command with no explicit `-mod` flag records that requirement and continues.

- The go command reads the `go.mod` file of every org module in the requirement graph. The highest version any of them requires for a module is the declared version.
- A module at its declared version may take its checksum from the checksum database, the same way `go mod download` does. A hash that disagrees with `go.sum` or with the database still fails.
- The command writes `go.mod` and `go.sum` when the whole change adds or raises `// indirect` requirements to their declared versions. It prints nothing about it.
- Any other change fails as `-mod=readonly` fails upstream: a new direct import, a module no org module declares, a changed `go` line, a lowered version.

## What stays upstream

- An explicit `-mod=readonly`, `-mod=vendor` or `-mod=mod` keeps its upstream meaning. So does a workspace.
- Vendor mode resolves nothing. `go.mod` and `vendor/modules.txt` both record the placeholder, and the vendored packages build as that version. That is how `src/cmd` builds, in CI as well.
- Modules that no org module declares follow the upstream rules exactly.

The script tests `src/cmd/go/testdata/script/org_branch_head.txt`, `org_declared_sync.txt` and `org_ci_build.txt` cover each case. `org_ci_build.txt` skips where a coding agent is an ancestor of the test, because the go command never makes a CI build there. `dats/checks/org-modules.dats` runs it on the runner. The unit tests for the lock are `src/cmd/go/internal/orgmod/runlock_test.go`.

## One resolution per CI run

Each go command resolves an org module to its branch head by itself. A CI run starts many of them, across many jobs. A commit that lands during the run then reaches some jobs and not others, and their outputs differ.

`GOORGPIN` fixes the versions for the whole run. It holds whitespace-separated `path=version` entries. The first job of the run resolves each org module once and passes the list to every other job. A listed module skips the branch head and uses the pinned version. The logic is in `src/cmd/go/internal/modload/orgpin.go`.

Only CI may pin. A pin anywhere else can hold an org module at an old commit to dodge a new one. The go command honors `GOORGPIN` only when `GITHUB_ACTIONS` is `true`, no coding agent marker is set, and no ancestor process is a coding agent. Anywhere else a set `GOORGPIN` is an error. A malformed entry is an error too. The agent roster mirrors `github.com/wow-look-at-my/is-this-an-agent`.

`src/cmd/go/internal/modload/orgpin_test.go` covers the rules. The script test `src/cmd/go/testdata/script/org_pin.txt` covers the refusals.
