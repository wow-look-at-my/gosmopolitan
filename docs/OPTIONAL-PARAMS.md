# Optional parameters

A parameter may carry a default:

```go
func ReadModulePath(root string = ".") string
```

A call may then omit it, and the compiler passes the default expression in its place:

```go
ReadModulePath()      // root == "."
ReadModulePath("sub") // root == "sub"
```

This removes the pattern the feature exists for: an API whose every production caller passes the same constant, parameterized only so a test can. Go's alternatives are a variadic tail, which accepts two roots and cannot say so, or a second exported function per default.

## Rules

- Only a **named ordinary parameter** takes a default. A type parameter list reads `=` as a type. An unnamed parameter gives the callee nothing to read. The parser rejects both.
- Defaults must be **trailing**. A call omits a suffix of the parameter list, so a default before a required parameter can never be used.
- A default is a **constant**, and a boolean, string or integer one. The call site is given the value spelled back as source, and only those three kinds have a spelling that reads back as the. A float can arrive as a rational.
- The constant must be **assignable to the parameter type**, by the ordinary assignability rules. An untyped one converts exactly as an argument can.
- A **variadic** parameter already has a default: no elements. The parser reads `...T` and stops there, so an `=` after one is a syntax error.

## Where it lives

| Step | Package | State |
|---|---|---|
| Parse `= expr`, carry it on `syntax.Field.Default`, print it again | `cmd/compile/internal/syntax` | done |
| Check the constant and enforce trailing | `cmd/compile/internal/types2` | done |
| Record it on the parameter (`Var.Default`) | `cmd/compile/internal/types2` | done |
| Accept a call that omits a defaulted suffix, and fill it | `cmd/compile/internal/types2` | done |
| Parse `= expr`, carry it on `ast.Field.Default`, print it again | `go/ast`, `go/parser`, `go/printer` | done |
| The same checks and the same fill | `go/types` | done |
| Carry it through export data | `cmd/compile/internal/noder`, both importers | done |

`src/internal/types/testdata/check/paramdefaults.go` is one file both checkers read, so a rule either package forgets shows up as a missing error in that package alone.

An `ast.Field` holds one default and however many names, so `go/parser` gives a parameter that has one a field of its own. `gofmt` therefore reprints `f(a, b int = 1)` as `f(a int, b int = 1)`. The two declare the same function, and no file without a default reformats at all.

The arity check and the lowering land **together**, in one place: `arguments` appends a synthesized literal to the call for each omitted parameter, before. So `noder` needs no change at all — it reads a call that is already full. A relaxed check with no fill can type-check a call and then emit one with too few arguments, which is a miscompile rather than.

## Export data

A default is part of a function's signature for a caller in another package, so it rides the unified IR. `pkgbits.V5` adds it: the writer emits a bool per parameter and the constant behind it, and the three readers take it back — `noder` for. A stream at an older version carries no such bit, so nothing there changes.

The compiler's own reader drops the value it reads. types2 fills an omitted argument before the IR exists, so every call `noder` sees is.

Three tests cover it. `test/paramdefaults.dir` compiles a library and a caller in separate packages and runs the result. `TestParameterDefaults` in `go/internal/gcimporter` reads the same defaults back off a compiled object through `go/types`. And `src/internal/types/testdata/check/paramdefaults.go` holds the rules both checkers enforce inside one package.
