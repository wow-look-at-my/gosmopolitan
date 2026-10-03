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
- The code of this tree gets no warning, because it is not the user's code. That is a package compiled with `-std`, and a source file under `$GOROOT`. The second covers an external test package of the standard library, which the go command compiles without `-std`, and the programs of the `test` directory. A body inlined from another function gets no warning either. The function it came from has its own.

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

## Profiling

Each stage of the pass is a phase of `-bench`. `scan` finds the calls, `compile` runs the compiler's regexp once per distinct pattern, and `emit` writes the read-only data and rewrites the calls. Each phase reports its counts:

```
go tool compile -p=main -importcfg=importcfg -o=main.o -bench=bench.txt main.go
grep regexp-precompile bench.txt
```

```
BenchmarkCompile:main:fe:regexp-precompile:scan     1   6125893 ns/op  0.07 %  10381 calls  ...  0 dynamic  ...  0 warnings  ...
BenchmarkCompile:main:fe:regexp-precompile:compile  1  66266561 ns/op  0.77 %  10381 patterns  ...
BenchmarkCompile:main:fe:regexp-precompile:emit     1 216165551 ns/op  2.52 %  10381 sites  ...  14633067 B  ...  63307 syms  ...
```

`calls` counts the calls to the functions, `dynamic` the calls that compile at run time, and `warnings` the performance warnings. `patterns` counts the distinct patterns, and `sites` the calls that now copy a template. `B` is the size of the symbols plus the string contents that they point to, before the linker merges equal strings. `syms` counts the symbols. The work from the pass up to escape analysis is `fe:pre-escape`, so no other step is charged to `emit`.

`-d=regexpprecompile=1` prints each precompiled site, with the time of its compile and the size of its data. A site whose pattern an earlier site already wrote says so. `-d=regexpprecompile=2` adds each site that stays dynamic, and the reason:

```
go build -gcflags=-d=regexpprecompile=2 ./cmd/app
```

```
./main.go:8:27: regexp precompile: regexp.MustCompile stays dynamic: the pattern is the parameter pat
./main.go:16:27: regexp precompile: regexp.MustCompile(`a+b`) precompiled: compile 5.961µs, 564 B read-only data
./main.go:20:27: regexp precompile: regexp.MustCompile(`a+b`) precompiled: compile 5.961µs, 564 B read-only data, shared with an earlier site
./main.go:28:23: regexp precompile: regexp.Compile stays dynamic: the pattern is invalid, and Compile returns the error at run time
```

Each stage is a function of its own. As a result, a CPU profile of the compiler names it: `regexpprecompile.(*pass).scan`, `(*pass).compilePatterns`, `(*pass).emitSites`, and `template` with the `(*emitter)` methods under it.

```
go tool compile -p=main -importcfg=importcfg -o=main.o -cpuprofile=cpu.prof main.go
go tool pprof -top -cum -focus=regexpprecompile cpu.prof
```

`go list -export -deps -f '{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}}' regexp > importcfg` writes the import config.

## Performance

Measured on linux/amd64 with this tree's toolchain. `BenchmarkMustCompile` and `BenchmarkPrecompiledMatch` in `regexp/precompiled_test.go` hold the patterns. A constant pattern gets the template, and the same pattern read from a map compiles at run time. `email` is the WHATWG email pattern. Its bounded repeats make a large program. The medians of multiple runs:

```
go test -c -o regexp.test regexp
./regexp.test -test.run='^$' -test.bench='MustCompile|PrecompiledMatch' -test.benchtime=500ms
```

```
# median of 8 runs
MustCompile       precompiled                 run time                       speedup
literal `hello`   126 ns   176 B   2 allocs     1.40 µs   1.35 KiB   17 allocs     11x
typical           143 ns   224 B   2 allocs    10.55 µs   9.22 KiB  111 allocs     74x
email             122 ns   176 B   2 allocs    68.62 µs  86.81 KiB  472 allocs    564x
url               174 ns   272 B   2 allocs    26.43 µs  21.79 KiB  340 allocs    152x

MatchString       precompiled   run time
literal           72.3 ns       72.1 ns
typical           369 ns        369 ns
email             669 ns        659 ns    (±11% and ±3%)
url               903 ns        910 ns
```

A precompiled call costs the copy of one Regexp and its SubexpNames slice, whatever the size of the pattern. Matching is the same within the noise, because both run the same program.

A program with 200 package-level `var re = regexp.MustCompile(...)` patterns of multiple shapes, built once with constant patterns and once with each pattern in a package variable. Init time is `GODEBUG=inittrace=1` for package main, the median of multiple runs:

```
var re0 = regexp.MustCompile("^k0_[a-z0-9._-]+@([a-z0-9-]+)\\.(com|org|net)$")  // precompiled
var pat0 = "^k0_[a-z0-9._-]+@([a-z0-9-]+)\\.(com|org|net)$"                    // run time
var re0 = regexp.MustCompile(pat0)

GODEBUG=inittrace=1 ./app 2>&1 >/dev/null | grep '^init main '
go build -ldflags='-s -w' -o app.stripped . && size -A app
```

```
# 200 patterns in 5 shapes; init is the median of 31 runs; speedup 92x
                 init main   init heap        file      stripped   .rodata    .text
precompiled      0.060 ms    44.8 KB  400     4.37 MB   2.60 MB    1.15 MB    677 KB
run time         5.5 ms      2.62 MB  29287   2.75 MB   1.77 MB    79 KB      783 KB
```

Init is many times faster and allocates 98% less. The binary is larger. Each template is read-only data, between 1 and 8 KB per pattern for these shapes. Each template object is a named symbol in the symbol table. The text is smaller, because the parser and the compiler of `regexp/syntax` are not linked. `-d=regexpprecompile=1` prints the data size of each site.

## Tests

- `TestRegexpPrecompileDifferential` in `cmd/compile/internal/test` reads the whole regexp test corpus. That is `re2-search.txt`, `re2-exhaustive.txt.bz2`, the Fowler `.dat` files, and the pattern tables of the `regexp` and `regexp/syntax` tests. It builds each one from a constant and from a variable, in both syntaxes. The two must be deeply equal and must give the same answer from every match method on every corpus input, with and without `Longest`.
- `TestRegexpPrecompileNoRuntimeCompile` builds a program from the constant patterns of the corpus, without the exhaustive file. It checks that `regexp.compile`, `regexp/syntax.parse`, `regexp/syntax.Compile` and `regexp.compileOnePass` are not linked. A program with one variable pattern must link all four, which proves the check can fail.
- `TestConstantPatternIsPrecompiled` in `regexp` checks that two calls with one constant share one program, which a run-time compile never does.
- `TestRegexpPrecompileWarning` in `cmd/compile/internal/test` builds a program outside GOROOT. The build must succeed and print the warning at the file and line of each dynamic call, and of no other.
- `TestRegexpPrecompileInstrumentation` in `cmd/compile/internal/test` compiles a file with each kind of site. It checks the `-bench` phases and their counts, and the lines of `-d=regexpprecompile=1` and `=2`.
- `test/regexpprecompile.go` checks the copy semantics, and `test/regexpprecompile_err.go` checks the compile error.
