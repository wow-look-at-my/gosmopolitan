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
- A default is an **expression**, not a constant. It is evaluated at the call
  site, in the caller's package, once per call that omits it — the same
  ordering an explicit argument would get.
- The expression must be **assignable to the parameter type**, by the ordinary
  assignability rules.
- A **variadic** parameter already has a default (no elements) and takes no
  `=`.

## Where it lives

| Step | Package | State |
|---|---|---|
| Parse `= expr`, carry it on `syntax.Field.Default`, print it again | `cmd/compile/internal/syntax` | done |
| Check the expression, record it on the parameter, enforce trailing | `cmd/compile/internal/types2` | to do |
| Accept a call that omits a defaulted suffix | `cmd/compile/internal/types2` | to do |
| Insert the default expressions at the call site | `cmd/compile/internal/noder` | to do |
| The same four for tooling | `go/ast`, `go/parser`, `go/types` | to do |

The arity check and the lowering must land **together**. A relaxed check on
its own type-checks a call and then emits one with too few arguments, which is
a miscompile rather than a missing feature.

## Export data

A default is part of a function's signature for a caller in another package,
so it has to survive export. Until it does, a default is usable only inside
the package that declares it.
