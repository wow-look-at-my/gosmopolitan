# The shared build cache

The cache is `github.com/wow-look-at-my/go-s3-server/cacheclient`, a submodule under `src/cmd/vendor/`. Every part of it lives there. That means the directory on disk, the store under that directory, the broker, and the layering between them. `cmd/go/internal/cache` is a view of it for the go command.

## What each side owns

`cacheclient` owns caching. Its `cachedisk` package is the half that speaks to no network, so a program forbidden to depend on `net` can hold a cache anyway. `cmd/go`'s bootstrap build is that program, and `store_bootstrap.go` is where it takes the directory alone.

| file | what it is |
|---|---|
| `cachedisk/diskcache.go` | the directory: entry files, output files, the trim |
| `cachedisk/disklock.go` | the lock a writer takes, and the atomic file write |
| `cachedisk/diskhash.go` | `HashSize`, and the verify-mode record of what produced a key |
| `cachedisk/tier.go` | `Tiered`, so a trace can name which tier answered |
| `disk.go` | those names again, for a consumer that holds the whole client |
| `web.go`, `webget.go`, `webput.go` | the store and its wire protocol |
| `storetier.go` | the store under the directory, and which one answered |
| `brokerowner.go` | the owner: the hello queue, and a channel per child |
| `brokerchild.go` | the cache a child holds, which is its channel to the owner |
| `brokerwire.go` | the record each request and each reply is |
| `brokerflight.go` | one lookup and one store per action, however many ask |
| `opencache.go` | `OpenCache`, which decides owner or child |

`cmd/go/internal/cache` owns what the go command knows and the cache does not.

| file | what it is |
|---|---|
| `gocache.go` | the type names, the delegating helpers, and `GetMmap` |
| `actionhash.go` | the action hash: what a key MEANS is the go command's |
| `store.go` | reading the environment, naming the target and module, and the notice sink |
| `store_bootstrap.go` | the directory alone, for a build that may not have net |
| `default.go` | `GOCACHE`, and opening the cache once per process |
| `trace.go` | recording each lookup onto a build's trace lane |

A change to how the cache behaves goes in `cacheclient` and rides its branch. See CLAUDE.md, "A cmd/go change that needs a new client API rides the client's own branch."

## One build, one owner

A build is not one process. A test suite starts thousands of go commands a minute. A cache each of them opens for itself charges each of them separately. Each pays an index write, a trim, a connection to the store, and an exit held open to drain its own uploads. Measured, that is about 0.75 s in any process with something to upload.

The first go command opens the directory. It puts the store under it and serves both over shared memory, through `github.com/wow-look-at-my/go-ipc`. `serveBroker` creates a hello queue and names it in `GO_BUILDCACHE_BROKER`, so every process it starts finds it. A child creates a duplex channel of its own and names that channel on the hello queue. The owner then opens it. The channel exists before the owner hears of it, so the open never races the create.

A child holds a `brokerCache`. It has no `DiskCache`, no key index, and no connection to the store. It asks for an action and gets back the identity of a file the owner has already written. It then opens that file. The layout is the owner's, and the child computes the same name from the same directory.

That leaves one writer for the directory. So the trim has a single owner. The invariant `Cache.Close` describes is then enforceable rather than hoped for.

The environment decides the shape:

- `GO_BUILDCACHE_BROKER` names a live owner's hello queue. A process that finds it set becomes a child, once the owner answers with the directory it writes into. A process that finds it unset becomes the owner. A name outlives the process that made it. So the owner's reply is what decides, never the name.
- `GO_BUILDCACHE_BROKER_OFF` makes every process open the cache for itself, which is what a bisect of a broker-shaped problem wants.

A test binary and a `go run` program are started with the environment the go command was started with. They do not get the go command's own environment. So `initDefaultCache` appends `cacheclient.BrokerEnviron()` to `cfg.OrigEnv`. A child started without those entries opens the directory for itself, which is the whole thing this exists to stop.

## Nothing spins

Every wait is a park. A send into a ring with room makes no system call at all. A side with nothing to read parks on a kernel wait rather than looking again. A second asker for an action already in flight blocks on `close(chan)`. Nothing polls, and no wait has a sleep in it.

`brokerflight.go` is the dedup, for both directions:

- **Get.** The first asker owns the flight and does the lookup. Everyone else parks on its channel. The owner closes the channel from a deferred call. A panic in the lookup then wakes the waiters rather than stranding them forever.
- **Put.** Two children that built the same object offer it at once. The first one's store is the one that happens. The rest park, then read the entry it produced. One body is read and stored one time.

The key leaves the map before the channel closes. The next ask therefore starts a flight of its own, rather than reading a result that is already spent.

## The channel carries no bodies

A request is a record: a correlation ID, the action ID, and a path for a put. A reply is the same ID, the output ID, the size, the mtime and the tier. The bytes are already in the cache directory, under the name the output ID gives them, and both processes can open that directory. A put names its body by path for the same reason. The owner reads that path before it answers. The child may therefore exit the moment `Put` returns.

A build compiles in parallel, so its replies come back in whatever order the owner finishes. The correlation ID is what puts each reply back in the hand of the caller waiting for it.

A caller holding an open file names that file. One holding anything else spills to a temporary file first. The bytes have to reach another process. A path is what this protocol carries.

## Tiers, and which one answered

`storeTier` puts the store under the disk cache. Disk stays authoritative. A hit there answers with no request at all. A body the store serves is written to disk before it is handed back. So a caller reads a file either way.

The look-ahead pool has somewhere to put what it fetches, which is what turns it on. `OnBatchEntries` writes an object to disk before the build asks for it. The ask is then a local read.

A hit served over the network reads exactly like one off local disk unless the cache says otherwise. That is the most useful thing a cache trace can say. So `Tiered` reports it. `DiskCache` answers `disk`. `storeTier` answers whichever tier served the body. `brokerCache` answers the tier the owner named in its reply. `cmd/go/internal/cache/trace.go` records it on the build's trace lane, one slice per lookup and per store.

## Configuration

`GO_BUILDCACHE_CONFIG` configures the store. `openStore` reads it, then names what this build IS: the target it produces (`GOOS/GOARCH`), the toolchain version, and the main module. The module is not known when the cache opens. So `modload` calls `cache.SetSharedModule` once it knows, and that reaches the open store.

A run with `CI` set and no configuration is an error rather than a quiet local build. A build that silently stops sharing is how a cache regression hides. A store that is configured but cannot be reached is a slower build rather than a broken one. It says so on stderr.

`GOCACHEDEBUG` makes the cache describe itself: which process owns it, what each tier answered, and what it moved.

A cache in trouble always reports. What `GOCACHEDEBUG` turns back on is the routine success reporting, which is the index size on every go command and a summary per batch.

`GOCACHELOG` names a file that takes those notices in place of stderr. A go command's output is DATA to whoever ran it, and tests across this tree run `go list` and read the answer. A routine line on that stream becomes a package name, a directory, or a file path somebody then opens. `dist test` sets the variable and prints the file at the end, so an outage reaches the build's output and never a test's.
