# Shared build cache

The org's shared build cache is reached in process. `cmd/go` requires `github.com/wow-look-at-my/go-s3-server/cacheclient` and calls it from `cmd/go/internal/cache/shared.go`, which layers a network tier under the disk cache: disk stays authoritative.

## `GOCACHEPROG` is deleted

The variable, the protocol, and `cmd/go/internal/cacheprog`. `chooseCache` (`cache/default.go`) picks the shared tier over disk, or disk alone. Nothing forks a cache program. A leftover `GOCACHEPROG` in the environment names nothing. The subprocess was the cost, not the feature. It answered with a PATH rather than bytes. A program storing bodies in packs had nowhere to put them.

## Configuration and reporting

`GO_BUILDCACHE_CONFIG` configures the tier (`cacheclient.ConfigFromEnv`). Unset. The build stays on disk. A run with `CI` set and no shared cache fails outright, because an unconfigured CI run decides whether every other CI run recompiles. A tier that cannot be reached leaves the build on disk and reports it. With `GOCACHELOG=<file>` set, the report goes to that file. `dist test` sets it for every go command it runs and prints the file on its own stderr at the end. So an outage never lands on the stderr a test compares. Unset, the report goes to stderr. Its ROUTINE success reporting is held back, because a go command's output is data somebody parses. `GOCACHEDEBUG` turns that back on.

## `make.bash` installs `std` and the tools with `$GOROOT/bin/go`, not `go_bootstrap`

bin/go is the only binary the build produces that carries the client. It is therefore the only one that can fetch that compile instead of repeating it. `go_bootstrap` installs `cmd/go` alone, which puts bin/go in place. `checkNotStale` then asserts that both drivers agree. Depth: docs/CI.md.

## `SharedCache.populate` is what makes look-ahead real

The client fetches objects ahead of the build on its own goroutines, and hands them to `OnBatchEntries`. Nothing else stores them. Leaving that nil turns look-ahead off, which is what it was for a long time: every prefetched body was parsed and dropped. `putVerified` writes a hit without recomputing the hash the client just checked. Depth, and the measurements: `docs/look-ahead.md` in the go-s3-server repo.

## An entry is bytes, under a key of source and compiler

Every entry is one plain file at `<outputID>-d`. There is no executable cache. `PutExecutable`, `GetExecutableFile`, the `ExecutableCache` interface and `Action.CacheExecutable` are all deleted. That path made an entry a DIRECTORY holding a file named for the binary. A name is not a property of the bytes, and it never went on the wire. So a shared hit restored a plain file that `exec` then refused. `go run` and `go tool` link the binary and run it from the build directory. `BuiltTarget` is the one answer either asks for.

## The client is never pinned

`src/cmd/vendor/github.com/wow-look-at-my/go-s3-server` is a submodule that tracks this repository's own branch, through `branch = .` in `.gitmodules`. A submodule with no branch of that name takes its default branch. A pin froze `shared.go` against a commit whose `WebSummary` carried no `HitBytes`, `PutBytes` or `IndexBytes`, so `go_bootstrap install cmd/go` failed and no toolchain built at all.
