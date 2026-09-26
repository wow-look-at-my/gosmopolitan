# Link-time collections

Status: design only. Nothing here is implemented.

A collection is a package-level slice that the **linker** fills. Each package in the program can add values to it. The linker gathers every value from every linked package. As a result, the slice is complete before any `init` runs. It replaces the "register yourself in `init()`" pattern.

## The pattern

A system needs several implementations of one thing, in no real order. An image library supports jpg, png and webp. Each loader lives in its own package and implements `Loader`. Each one registers itself with a manager in `init()`:

```go
// image/loader.go
type Loader interface {
	Match(hdr []byte) bool
	Decode(r io.Reader) (Image, error)
}

var (
	mu      sync.Mutex
	loaders atomic.Value // []Loader
)

func Register(l Loader) {
	mu.Lock()
	defer mu.Unlock()
	old, _ := loaders.Load().([]Loader)
	loaders.Store(append(old[:len(old):len(old)], l))
}

// image/png/png.go
func init() { image.Register(pngLoader{}) }

// main.go
import _ "image/png" // without this line, png does not exist
```

The standard library does this in `image.RegisterFormat`, `database/sql.Register`, and `encoding/gob.Register`.

## Problems with the pattern today

1. **The blank import is an invisible dependency.** Forget `import _ "image/png"` and the program still builds. It fails at run time with `image: unknown format`. goimports cannot add the import, because nothing names the package. Linters dislike blank imports. The source does not say why the line is there.
2. **The order is arbitrary but not random.** `image.sniff` (`src/image/format.go`) returns the first registered format whose magic matches. Since Go 1.21, packages with no dependency between them initialize sorted by import path. So a package rename changes which decoder wins.
3. **A duplicate is fatal or silent.** `database/sql` panics with `sql: Register called twice for driver`. The panic happens in `init`, before `main`, and nothing can recover it. `image` appends the duplicate silently. Neither is caught at build time, although the build knows every registrant.
4. **The registry is mutable global state.** Every registry writes its own mutex, `atomic.Value`, copy-on-append, lookup, duplicate check and sorted listing. All of that protects data that never changes after startup. A test cannot get an isolated registry. A program cannot have two.
5. **Init ordering is fragile.** The registry must exist before a registrant's `init` runs. That holds only because the registrant imports the registry package. Code that reads the registry in its own `init` sees a partial list, depending on where it sits in the order.
6. **The linker cannot drop an unused loader.** An `init` always runs. So everything it references stays in the binary, even when nothing calls `image.Decode`.
7. **No tool can see the set.** Nothing at build time can answer "which loaders are in this program". The answer exists only at run time.
8. **The interface check is by hand.** Authors write `var _ Loader = (*pngLoader)(nil)` to get it. The register call accepts whatever its parameter type accepts. `gob.Register` takes `any`.

Startup time is not on this list. A registration call is cheap. It matters only with thousands of registrants.

The root cause of all eight: a static fact ("these loaders are in this program") is computed at run time, by side effects, in an order nobody chose.

## Prior art

| System | Mechanism | What to keep, what to avoid |
|---|---|---|
| C++ static registrars | The constructor of a global object inserts into a registry at startup | Works. A function-local static registry handles cross-TU order. The costs are the ones Go's `init()` pattern has: any code can insert at any time, so a reader must lock and can see a different list on each read. The list is complete only after every constructor has run. A static library drops an object file that nothing references, which is a linker defect. Collections fix the Go equivalent: a read of the collection keeps every contribution. |
| Rust `linkme::distributed_slice`, `inventory` | Each crate puts elements in a linker section. The linker concatenates them into one slice. | Keep. The slice is immutable, complete before any code runs, and known at build time. Order is link order, which is the weak point. |
| Linux kernel initcalls | Linker sections, grouped by level | The same idea, with priority as explicit levels. |
| Java `ServiceLoader` | Everything on the classpath is loaded | Avoid. Present is not the same as chosen. Reflective, at run time. |
| Go's own linker | Assembles typelinks and itablinks. Orders init tasks (`cmd/link/internal/ld/inittask.go`). | The machinery exists. This feature gives it to user code. |

## The design

```go
// image/loader.go
collect var Loaders []Loader

// image/png/png.go
func init() {
	image.Loaders += pngLoader{}
}

// consumer
for _, ldr := range image.Loaders {
	...
}
```

### Rules

- **`collect var` declares a collection.** `collect` is a contextual keyword, like `readonly` (docs/READONLY-VARS.md). It is a keyword only where a top-level declaration starts, and only before `var`. The type must be a slice.
- **A contribution is `X += value`. It is legal only in `init()`.** It must be an unconditional statement at the top level of an `init` body. It is a compile error inside an `if`, a loop, a closure, or any other function: `image.Loaders: contribute only at the top level of init`.
- **The value must be statically initializable.** This is the test `staticinit` already applies to `var x = T{...}`. `image.Loaders += newLoader()` is a compile error. Expensive state goes behind `sync.OnceValue` inside the methods.
- **Each contribution is type-checked against the element type.** The `var _ Loader = ...` line is no longer needed.
- **The collection is readonly everywhere, the declaring package included.** The backing array is in rodata. An assignment to the variable, an assignment to an element, and an `append` onto it are compile errors. This is stricter than `readonly var`, where slice elements stay writable.
- **Order is stable and meaningless.** The linker sorts by package path, then by source position. The documentation states that this order is not a priority. A consumer that needs priority puts a priority field in the element and sorts on it.

### The lift, and why it is acceptable

The statement is written in `init`, but the compiler lifts it into a link-time section. It does not execute when `init` runs. One consequence is visible: `image.Loaders` is complete when any `init` reads it, the declaring package's own `init` included.

Go already works this way. The spec says `var x = T{...}` is initialized by package initialization. The compiler emits data instead.

The alternative is a real append at run time. It keeps the syntax literal. It also brings back problems 2, 4 and 5.

An `init` that holds only contributions becomes empty. The compiler already removes an empty `init`.

### How each problem is handled

| Problem | Handling |
|---|---|
| 1. Blank import | Still the way to choose. A read collection with no contributor fails the build. See "What go-toolchain does". |
| 2. Order | Stable, documented as meaningless. Priority is an element field. |
| 3. Duplicates | Not detected. Keyed collections are deferred. See "Deferred". |
| 4. Mutable global state | A contribution compiles only at the top level of `init()`. No other code can add to the collection, so no reader needs a lock and every read after `init` sees the same list. For test isolation, an API takes the slice as a parameter that defaults to the collection (docs/OPTIONAL-PARAMS.md): `func Decode(r io.Reader, loaders []Loader = Loaders)`. |
| 5. Init ordering | Complete before any `init` runs. |
| 6. Dead code | If nothing reads the collection, the linker drops it and every contribution. |
| 7. Visibility | Contributions are in export data. go-toolchain prints them. |
| 8. Interface check | Each contribution is type-checked. |

### Linker mechanics

- Each contribution becomes a symbol `go:collect.<pkgpath>.<name>.<n>`. The compiler records it in the object file and in export data (a `pkgbits` bump, as `readonly` needed).
- The linker sorts the contributions for each collection and concatenates them into one read-only array. This is the same pattern as itablinks.
- Plugin and shared build modes: each module carries its own section. The runtime merges them across `moduledata`, as it does for typelinks. Cosmo has no plugin mode. As a result, this is only for upstream parity.
- Fat APE: each payload links separately. Both payloads get identical collections.

## What go-toolchain does

go-toolchain is the only tool a user runs. Everything here happens with no flag.

- **A read collection with no contributor fails the build.** The linker error: `image.Loaders is read but no linked package contributes to it; import one (e.g. image/png)`. This replaces the run-time `unknown format`. It does not choose loaders for the user. The import is still the choice.
- **go-toolchain never removes a contributing import.** Its goimports pass treats a blank import of a package that contributes to a read collection as needed.
- **The build output lists each collection and its contributors,** one line per collection, the way go-toolchain prints coverage.

## Selection stays with the import

Linking every contributor in the module graph automatically is the `ServiceLoader` mistake. `require` lines include test-only and indirect modules. Present in `go.mod` is not the same as chosen. So the import still decides what is linked.

The idiom for "every format" is an umbrella package, such as `image/all`, that imports each one.

## Deferred

- **Keyed collections.** `collect var ByExt map[string]Loader` with `image.ByExt[".png"] = pngLoader{}`. The linker will build a sorted key table or a perfect hash. A duplicate key will be a link error that names both contributing packages. This is the fix for problem 3.
- **A dynamic overlay.** A library type such as `registry.Set[K, V]` for a plugin loaded at run time or a value from a config file. Its static base is a collection, with a mutable layer on top.

## Rejected: reflection over every implementation

The alternative is a reflection call that returns every linked type that implements an interface, such as `reflect.Implementations[Loader]()`. It is rejected. Go interfaces are satisfied by method shape, not by declaration, so "implements" does not mean "meant to be registered".

1. **It returns types nobody meant to register.** Test mocks, decorators such as `loggingLoader{inner Loader}`, caching wrappers, and adapters in unrelated libraries all match. A wrapper's zero value is useless (a nil `inner`), and it sorts into the list by name. A small interface is worse: every `io.Reader` is hundreds of types. Filtering them needs an explicit opt-in, which is a collection again, harder to see.
2. **It returns types, not values.** `reflect.New(t).Elem().Interface()` works only if the zero value is a working loader. A loader with configuration has no way to receive it. A collection holds values, or `func() Loader` constructors, so the author decides how it is built.
3. **`T` or `*T`.** With pointer receivers only `*T` matches. With value receivers both match. The result has duplicates, or needs a rule to drop one.
4. **It fights dead-code elimination.** The linker will have to keep every linked type whose method set matches, and those methods. That keeps the mocks and wrappers from point 1.
5. **Generics.** An uninstantiated generic type has no type descriptor. A generic loader is found only when some other code happened to instantiate it.
6. **It hides the dependency.** Nothing in `png.go` says the type is registered. A method rename silently removes it from the set, with no error.

It also does not fix problem 1. A type is in the binary only if its package is imported. As a result, the blank import stays. It removes one line per implementation, the `+=`, and in exchange the author can no longer control what is in the set.

A variant with an explicit opt-in marks the type instead:

```go
type pngLoader struct{ image.Registered } // embedding is the opt-in
```

The collection will then be every linked type that embeds the marker. It is opt-in and visible. It moves the `+=` into the struct definition and keeps points 2 and 3. The `+=` in `init` is preferred.

## Decisions recorded

| Question | Decision |
|---|---|
| Where a contribution is written | In `init()`, as `X += value`. The compiler lifts it into data. |
| Slice order | Stable by package path and source position. Meaningless by contract. |
| First-version scope | Slice collections only. |
| Tooling | Automatic, through go-toolchain only. No user-facing flag. |
| Reflection over implementations | Rejected. |
