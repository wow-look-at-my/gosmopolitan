# Optional parameters

A parameter may carry a default:

```go
func ReadModulePath(root string = ".") string
```

A call may then omit it, and the compiler passes the default expression in
its place:

```go
ReadModulePath()      // root == "."
ReadModulePath("sub") // root == "sub"
```

This removes the pattern the feature exists for: an API whose every
production caller passes the same constant, parameterized only so a test can
point it somewhere else. Go's alternatives are a variadic tail, which accepts
two roots and cannot say so, or a second exported function per default.

## Rules

- Only a **named ordinary parameter** takes a default. A type parameter list
  reads `=` as a type, and an unnamed parameter gives the callee nothing to
  read; the parser rejects both.
- Defaults must be **trailing**. A call omits a suffix of the parameter list,
  so a default before a required parameter could never be used.
- A default is a **constant**, and a boolean, string or integer one. The call
  site is given the value spelled back as source, and only those three kinds
  have a spelling that reads back as the same constant. A float would arrive
  as a rational.
- The constant must be **assignable to the parameter type**, by the ordinary
  assignability rules. An untyped one converts exactly as an argument would.
- A **variadic** parameter already has a default (no elements) and takes no
  `=`.

## Where it lives

| Step | Package | State |
|---|---|---|
| Parse `= expr`, carry it on `syntax.Field.Default`, print it again | `cmd/compile/internal/syntax` | done |
| Check the constant and enforce trailing | `cmd/compile/internal/types2` | done |
| Record it on the parameter (`Var.Default`) | `cmd/compile/internal/types2` | done |
| Accept a call that omits a defaulted suffix, and fill it | `cmd/compile/internal/types2` | done |
| The same for tooling | `go/ast`, `go/parser`, `go/types` | to do |

The arity check and the lowering land **together**, in one place: `arguments`
appends a synthesized literal to the call for each omitted parameter, before
it counts them. So `noder` needs no change at all — it reads a call that is
already full. A relaxed check with no fill would type-check a call and then
emit one with too few arguments, which is a miscompile rather than a missing
feature.

## Export data

A default is part of a function's signature for a caller in another package,
so it has to survive export. Until it does, a default is usable only inside
the package that declares it. A call from another package still compiles: it
sees a parameter with no default, and has to pass every argument.
