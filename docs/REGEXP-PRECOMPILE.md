# Precompiled regexp patterns

A call to `regexp.MustCompile`, `regexp.MustCompilePOSIX`, `regexp.Compile` or `regexp.CompilePOSIX` with a pattern the compiler can resolve compiles at build time. The program parses nothing and compiles nothing for it at run time:

```go
var word = regexp.MustCompile(`(?P<word>\w+)\b`) // built by the compiler

func find(s string) []string {
	return word.FindAllString(s, -1)
}
```

## What the compiler resolves

- A constant: a literal, a named constant, a constant expression or a concatenation of constants.
- A local variable that has one assignment and no other write, with a value the compiler resolves.
- The result of an inlined call. The pass runs after inlining, so `wrap("x+")` precompiles when `wrap` passes its argument to `MustCompile` and inlines.
- A `+` of any of these.
- A function value: `compile := regexp.MustCompile; compile("x+")` resolves like a direct call.

A package variable, a field, a parameter of a function that is not inlined, and anything read at run time stay dynamic.

## Every valid pattern precompiles

The pass has no list of supported constructs. It runs the `regexp` package that is linked into the compiler on the pattern and gets the same `*regexp.Regexp` that the program will get. Then it writes that object graph out as read-only data with a generic walk over the target's struct types. The `syntax.Prog`, the one-pass program, the prefix, the capture names, the start condition, the machine pool index and every other field. One-pass and backtrack programs, `\b`, `(?i)`, Unicode classes, named groups and POSIX leftmost-longest all reach the same code path. A pattern that `regexp.Compile` accepts precompiles. One that it rejects does not.

The call becomes a call to `regexp.precompiled`, which copies the template. Each call returns its own `*Regexp`, so `Longest` on one result changes no other. `SubexpNames` returns a slice that is copied for each call too, because the template is read-only. The original argument stays in the call, so its evaluation and its side effects stay where the source put them.

Equal patterns share their data. The symbols are named by a hash of the syntax and the pattern. They are `DUPOK`. As a result, the linker keeps one copy for the whole program.

## Errors and warnings

- An invalid pattern in `MustCompile` or `MustCompilePOSIX` is a compile error, because that call can never succeed:

  ```
  ./main.go:9:28: regexp.MustCompile(`a(`) always panics: error parsing regexp: missing closing ): `a(`
  ```

- An invalid pattern in `Compile` or `CompilePOSIX` is legal. The call compiles at run time and returns the error, as it does upstream.
- A `MustCompile` or `MustCompilePOSIX` pattern that stays dynamic compiles at run time, as it does upstream. The compiler prints a performance warning with the call's position:

  ```
  # example.com/app
  ./main.go:14:27: performance warning: the pattern of regexp.MustCompile is not constant, so it compiles at run time
  ```

  The warning never fails a build. The go command prints it for every build of the package, a cached one included. This is because it replays the compiler's output. go-toolchain streams that output during its build phase. `Compile` gets no warning: it is the function for a pattern that arrives at run time.
- The code of this tree gets no warning, because it is not the user's code. That is a package compiled with `-std`, and a source file under `$GOROOT/src`. The second covers an external test package of the standard library, which the go command compiles without `-std`. A body inlined from another function gets no warning either. The function it came from has its own.

## The compiler and the target must match

The compiler builds the template with its own copy of `regexp`, `regexp/syntax` and `unicode`. It writes the data with the layout of the target's `regexp.Regexp`. The walk matches each field by name and kind. A layout that differs stops the build with an internal error. A change to `regexp`, `regexp/syntax` or `unicode` therefore needs a toolchain rebuild, which `make.bash` does.

The bootstrap compiler, toolchain1, links the bootstrap Go's `regexp` and its older Unicode tables. It does not precompile (`base.CompilerBootstrap`). Every later stage does.

`Compile`, `CompilePOSIX`, `MustCompile` and `MustCompilePOSIX` carry `//go:noinline`, so the pass still finds each call after inlining.

## Where it lives

| Step | File |
|---|---|
| Find the calls, resolve the pattern, warn, report errors | `cmd/compile/internal/regexpprecompile/precompile.go` |
| Run the pass after inlining | `cmd/compile/internal/gc/main.go` |
| Copy a template | `regexp/precompiled.go` |

## Tests

- `TestRegexpPrecompileDifferential` in `cmd/compile/internal/test` reads the whole regexp test corpus. That is `re2-search.txt`, `re2-exhaustive.txt.bz2`, the Fowler `.dat` files, and the pattern tables of the `regexp` and `regexp/syntax` tests. It builds each one from a constant and from a variable, in both syntaxes. The two must be deeply equal and must give the same answer from every match method on every corpus input, with and without `Longest`.
- `TestRegexpPrecompileNoRuntimeCompile` builds a program from the constant patterns of the corpus, without the exhaustive file. It checks that `regexp.compile`, `regexp/syntax.parse`, `regexp/syntax.Compile` and `regexp.compileOnePass` are not linked. A program with one variable pattern must link all four, which proves the check can fail.
- `TestConstantPatternIsPrecompiled` in `regexp` checks that two calls with one constant share one program, which a run-time compile never does.
- `test/regexpprecompile.go` checks the copy semantics, `test/regexpprecompile_warn.go` the warning and `test/regexpprecompile_err.go` the compile error.
