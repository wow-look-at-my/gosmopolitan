# gopls against this toolchain

gopls is the Go language server. It parses and type-checks with the `go/*` packages of the toolchain that BUILDS it. So a gopls built by this fork knows what a fork source file means. A stock gopls does not. It reads a parameter default as a syntax error.

The fork of `golang.org/x/tools` that carries the matching changes is **wow-look-at-my/gosmopolitan_tools**.

## Build it

```bash
cd src && ./make.bash                       # build the toolchain
export PATH="$PWD/../bin:$PATH"
git clone https://github.com/wow-look-at-my/gosmopolitan_tools
cd gosmopolitan_tools/gopls
GOOS=linux GOARCH=amd64 go build -o ~/bin/gopls .   # or darwin/arm64
```

Pin `GOOS` and `GOARCH` to the host. The fork defaults to `GOOS=cosmo`. An APE cannot be the editor's language server. Point the editor's `gopls` setting at the binary this produces. Put the fork's `bin` on the PATH of the editor process, so gopls asks the fork's `go` about the workspace.

An editor that runs `gopls` against a cosmo workspace needs nothing else. The server never runs the code it analyzes, so the `misc/cosmo` exec wrappers do not enter the picture.

## What had to change, and why

Every item below is a fork change. Upstream gopls needs none of them, because upstream Go has no parameter defaults.

| Change | Where | What breaks without it |
|---|---|---|
| VERSION carries its suffix after a dash | `VERSION` | gopls's go.mod requires `go 1.27.0`. The old `go1.27.0cosmo` parsed as no version at all, so cmd/go reported the development version `1.27` and refused to build gopls. |
| The checker stops rewriting the call | `go/types` | gofmt-on-save turned `Greet()` into `Greet("world", false)`. gopls formats the AST it type-checked, so a fill written into `CallExpr.Args` reaches the user's file. |
| Defaults ride the indexed export data | `internal/gcimporter` (tools) | gopls caches each package as shallow export data. A default dropped there makes a call in ANOTHER package report a missing argument. |
| The unified reader matches this compiler | `internal/pkgbits`, `internal/gcimporter` (tools) | Upstream spends bitstream V5 on a method index. This fork spends V5 on parameter defaults. A reader that takes the wrong one desynchronizes rather than failing. |
| SSA reads defaults off the signature | `go/ssa` (tools) | The builder indexed one argument per parameter. A call that omits one ran past the end, so every SSA-based analyzer failed on that file. |
| The inliner supplies the omitted arguments | `internal/refactor/inline` (tools) | "Inline call to f" asserted that arguments and parameters match. |
| The signature shows the default | `gopls/internal/golang` | Hover and signature help read `f(name string)`, which does not say the argument is optional. They now read `f(name string = "world")`. |

## Checking it works

```bash
gopls check ./...                  # no diagnostics on a call that omits a default
gopls format -d main.go            # empty: the call keeps the arguments you wrote
gopls signature main.go:12:24      # shows "= <default>" on an optional parameter
```

The tools repo gates the same behavior on every push. Its `.github/workflows/cosmo-ci.yml` installs the published toolchain, builds gopls with it, and runs the tests for each package in the table.

## Delve, and other tools that read a binary

A tool that consumes a fork BINARY is a separate question from one that reads fork SOURCE. The pclntab format has diverged from upstream, so a tool that parses it needs the fork's own `debug/gosym`. The DWARF sidecars are untouched, which is why gdb and delve work. See the pclntab bullet in CLAUDE.md.
