# Adding Cosmo Support to Standard Library Packages

When a stdlib package fails to build for `GOOS=cosmo`, follow these steps:

## 1. Identify Build Constraint Types

Go uses types of build constraints:
- **`//go:build` directives** - Add `cosmo` to the constraint (e.g., `//go:build cosmo || linux || ...`)
- **Filename suffixes** - Files like `foo_linux.go` only build for Linux. Create `foo_cosmo.go` with equivalent functionality.

## 2. Check What Cosmo Already Has

Before creating new files, check existing cosmo implementations:
```bash
ls src/**/\*cosmo\*.go
grep -r "//go:build.*cosmo" src/
```

## 3. Runtime Platform Handling

Cosmopolitan binaries run on Linux, macOS, AND Windows at runtime, so never bake in single-OS assumptions. When creating `_cosmo.go` files:
- Do not assume Linux-only features like `/proc` are available
- Cosmopolitan Libc translates Linux syscalls to native OS calls at runtime
- Test assumptions about what works on each platform

## 4. Syscall Wrappers

The `syscall` package uses `//sys` comments to generate wrappers. Check:
- `src/syscall/syscall_cosmo.go` - main syscall implementations
- `src/syscall/zsyscall_cosmo_amd64.go` - generated syscall stubs

If a function like `Listen` is defined as lowercase `listen` but callers expect uppercase `Listen`, add a wrapper:
```go
func Listen(s int, backlog int) (err error) {
    return listen(s, backlog)
}
```

## 5. Common Patterns

When adding cosmo to an existing `//go:build` constraint, use alphabetical order:
```go
//go:build cosmo || dragonfly || freebsd || linux  // cosmo first alphabetically
```

For filename-based constraints, create new files rather than modifying the build system.
