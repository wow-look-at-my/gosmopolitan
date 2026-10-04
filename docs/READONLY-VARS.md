# Readonly variables

A package-level variable may be declared `readonly`:

```go
readonly var GOOS string = "cosmo"

readonly var (
	Count int
	Names []string
)
```

Inside the declaring package it is an ordinary variable: assign it, take its address, increment it. Every other package sees a value. An assignment, an address, an increment, or a method with a pointer receiver is a compile error there:

```
cannot assign to runtime.GOOS: it is readonly outside package runtime
```

This exists for `runtime.GOOS` and `runtime.GOARCH`. Both are variables on the cosmo port, because one APE boots on several kernels and the host is only known at startup. A package that writes to either makes every other package lie about the machine. A constant cannot be set at startup. A getter breaks every `runtime.GOOS` in the wild. `readonly` keeps the name, keeps the variable, and moves the guard into the compiler.

## Rules

- `readonly` is a keyword only where a top-level declaration starts, and only before `var`. A variable, a field, or a function named `readonly` stays legal. `readonly const`, `readonly type` and `readonly func` are syntax errors.
- A `readonly var` group marks every variable in the group.
- Only a package-level variable takes it. A local `readonly var` is a syntax error, because a statement never starts with the keyword.
- Reads are unchanged, and so is a reference in a constant context: `runtime.GOOS` still folds to the build value there (`types2/dynconst.go`).
- A field of a readonly struct variable is readonly too, because the whole variable is a value. An element of a readonly slice or map is not: the slice header is the value, and it still points at writable storage.

## Where it lives

| Step | Package |
|---|---|
| Parse the contextual keyword, carry `VarDecl.Readonly`, print it | `cmd/compile/internal/syntax` |
| Parse, carry `GenDecl.Readonly`, print it | `go/ast`, `go/parser`, `go/printer` |
| Record `Var.Readonly`, read the variable as a value from another package, name it in the error | `cmd/compile/internal/types2`, `go/types` |
| Carry the bit through export data (`pkgbits.V6`) | `cmd/compile/internal/noder`, both importers |

Tests: `test/readonly.dir` (the refusal, in another package), `src/internal/types/testdata/check/readonly.go` (the declaring package), and `TestReadonlyVars` in `go/internal/gcimporter` (export data).
