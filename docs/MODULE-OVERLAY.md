# Completing a fetched module

A module zip carries no generated file. So a dependency that generates part of its own API ships a package the compiler reads as empty. Every consumer then fails on a symbol that the package's source never declares.

The go command completes such a module when it fetches it. It runs the module's own `go:generate` directives once, over the whole module. It adds what they wrote to the extracted directory.

## The base zip and the overlay zip

The **base zip** is what the proxy serves, or what the repository holds. It is verified against `go.sum` and the checksum database exactly as before. Nothing about it changes. It is never uploaded anywhere. The proxy serves it already, and `go.sum` pins it already.

The **overlay zip** holds only the files the generators ADD. It is the one part the shared cache keeps.

A file the base zip carries keeps the base zip's bytes, whatever a generator writes over it. What a module's authors published is the module. Regenerating `golang.org/x/text` from today's Unicode data is the author's workflow. It is not a build step. A build that did it depends on the day it ran.

## The cache entry

There is one entry per module version. The key is:

    hash("modzip", "overlay v1", path, version, baseSum)

`baseSum` is the `h1` checksum `go.sum` recorded for the base zip. A module fetched straight from its repository has no such checksum. Its pseudo-version names the commit instead.

The body is a header line `overlay v1 <sum>` followed by a zip of the added files. Each file is named as the module's own zip names it. `<sum>` is the `h1` over those files.

An empty overlay is stored too. It is what says the module needs nothing. It is also what lets the next build skip generation entirely.

## Where it happens

`modfetch`'s `unzip` extracts the base zip. It then completes the directory, while the `.partial` marker still stands. No other process reads a directory that has the base files and not the generated ones. A completion that fails takes the directory with it, so nothing half-built survives.

`<version>.complete` records the `h1` of the completed directory. `go mod verify` holds the directory to that checksum when one is recorded. It holds the directory to the zip's own checksum when none is. Every checksum covers everything it names. Nothing is excluded from any of them.

## One version, one source tree

Everything above rests on one property. A module version means the same bytes on every machine. These rules keep that true.

**The module's own bytes decide which directives exist.** Nothing asks what this machine has installed. A scan that asked will make one module version mean different things on different machines. Both answers then land under the one key the whole fleet reads.

**A program a directive names and this machine lacks is a loud failure.** The failure names what to install. Skipping the directive hands this build a module that the same version elsewhere does not match.

**The store is guarded.** An entry already under the key must hold the same files. If it does not, the build stops and names the file that differs. That catches a generator whose output is not deterministic. It runs on every build that generates.

## No setting

There is no setting. Every fetch completes the module it fetched. No environment variable changes that. None is read.

A generator is a program. So the go command a directive starts fetches the generator's own dependencies, and it completes those too. That is the same work one level down. It ends where the dependency graph ends.

A module version is completed one time. `unzip` holds that version's lock file across the completion. A second go command waits for the first one.

Nothing here is a knob to turn off. A build that skipped the generators will compile a different package from the same module version. That is the one thing this mechanism exists to prevent.

## Sandboxing

A directive is a command a dependency's author wrote. A build runs it without anybody reading it first. So it runs confined. It may write the tree it generates and the caches a go command needs, and nothing else. The network stays reachable. A generator that fetches its own inputs is the case this exists for.

`generatesandbox.go` picks the backend. Linux uses bubblewrap. macOS uses seatbelt.

A host with no backend at all cannot confine a generator. That is fatal. It is not a reason to run one unconfined for a module.
