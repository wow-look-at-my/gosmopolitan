# Completing a fetched module

A module zip carries no generated file. So a dependency that generates part of its own API ships a package the compiler reads as empty. Every consumer then fails on a symbol that package never declares. `cmd/go` runs that module's own `//go:generate` directives when it fetches it. It adds what they wrote to the extracted tree. The code is `cmd/go/internal/gendep`, driven from `cmd/go/internal/modfetch/overlay.go`.

## Which modules generate

A directive is a command the module's author wrote. Completing a module runs it on this machine. Nobody reads it first.

- A module under `github.com/wow-look-at-my/` generates. That author is this fleet.
- Every other module generates only when its own `go.mod` carries a whole-line `//go:gendep` comment. The marker is the whole comment. A `go.mod` that mentions it in a sentence asks for nothing.
- A module with no `go.mod` has nowhere to write the marker. It never generates unless its path is the org's.
- A skipped module is named on stderr. The build compiles the zip as published. What a consumer sees is an undeclared symbol.

`gendep.Allowed` decides this from the module's own bytes alone, the way the directive count does. One module version therefore means one thing to every machine that reads the one overlay cache key.

## What a run may touch

Each package that carries a directive generates on its own, in path order. The run happens in a staged copy of the fetched module, never in the tree other builds compile from. The command runs confined: bubblewrap on a Linux host, seatbelt on a macOS one. It may write the staged tree and the caches a go command needs, and nothing else. The network stays reachable, because a generator that fetches its own inputs is the case this exists for.

A file the zip carries keeps the zip's bytes, whatever a generator wrote over it. What the module's authors published is the module.

A directive that fails stops the build and names the package. Some failures belong to this machine rather than to the module. A directive naming a program that is not installed is skipped. A host with no sandbox backend fails with a message that says so.

## The overlay cache

The files the generators add are stored in the build cache as the module's overlay. The key is the module path, its version and its base zip's checksum. A module cache that has never fetched the module takes the files from there instead of running anything. An answer that skipped a directive for a missing program is marked partial and is never stored. Such an answer describes this machine, not the module.

A second run that produces different files for one key stops the build and names the file that differs.

## Tests

`cmd/go/testdata/script/generate_dependency*.txt`, with their fixture modules under `cmd/go/testdata/mod/`. `generate_dependency_optin.txt` covers a module that never asked. `generate_dependency_nomod.txt` covers a module with no `go.mod` to ask in.
