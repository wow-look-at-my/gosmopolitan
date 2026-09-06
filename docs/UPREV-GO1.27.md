# Uprevving the fork to go1.27.0

This is the record of the go1.26.5 -> go1.27.0 merge. CLAUDE.md's "Uprevving
to a new upstream Go release" section is the procedure; this file is the
detail a minor-version bump needs and a patch bump does not.

## Why a minor bump conflicts and a patch bump does not

The fork's history merges release TAGS. A patch tag (go1.26.4, go1.26.5) sits
on `release-branch.go1.26`, so merging the next patch tag replays a handful of
backports and conflicts on VERSION alone. A minor tag sits on a NEW release
branch cut from master, so the merge base moves back to the point where
`release-branch.go1.26` left master. Every backport the fork already carries
then meets upstream's own version of the same change, and git reports it as a
conflict even though nobody disagrees.

go1.26.5 -> go1.27.0: 1,748 upstream commits, 73 conflicted files, of which 29
were pure release-branch-versus-master noise.

## Triage: separate noise from real disagreement

```sh
git diff --name-only go1.26.5 HEAD -- <conflicted file>
```

An empty result means the fork never edited that file since the previous tag,
so it has no stake in the conflict and upstream's side is correct outright:

```sh
git show :3:<file> > <file>   # stage 3 is "theirs"
```

29 of the 73 files resolved this way, including all of `crypto/tls`,
`internal/poll`, `os/root_openat.go` and the SIMD generator output. Do this
pass first; what remains is small enough to read.

## Resolutions worth knowing about

- **`src/net/http/h2_bundle.go` is gone.** go1.27 deleted the generated
  bundle and moved HTTP/2 into `src/net/http/internal/http2/`, an ordinary
  in-tree package. `golang.org/x/net/http2` is no longer synchronized with
  std. The fork never edited the bundle, so the deletion is accepted as-is.
- **`runtime/os_linux32.go` was upstreamed.** The fork's kernel-version
  selection of the 64-bit time syscalls is now upstream's code, so upstream's
  file wins whole; it additionally replaces the `setNsec` downgrade with
  `mustDowncastToTimespec32`.
- **The fork's vendored `x/tools` backports are superseded.** go1.27 vendors
  a newer x/tools that already contains them. After resolving, the vendored
  tree matches go1.27.0 byte for byte; only `klauspost/compress` (the linker's
  zstd DWARF encoder) is re-added on top of upstream's module files.
- **`funcNamePiecesForPrint` returns five pieces now**, not three. The fork's
  `printFuncName` keeps its table-aliasing prefix argument and passes all
  five.
- **`adjustframe` moved its dead-frame check** below the frame-pointer
  adjustment. The fork's non-allocating `funcnamePieces` debug print moves
  with it: `funcname` allocates in this fork, and allocating during a stack
  copy is not an option.
- **`mkinlcall` takes both `loopDepth` and `profile`.** The fork threads the
  first for loop-aware inlining; go1.27 added the second.
- **wasm opcodes are a union.** The fork's threads-proposal atomics (0xFE)
  and upstream's SIMD (0xFD) both extend the same enum. `a.out.go`, `anames.go`
  and `writeOpcode`'s range chain must agree on the order. `anames.go` is
  generated: check the union with
  `go run ../stringer.go -i a.out.go -o <tmp> -p wasm` and diff.

## Internal APIs a minor bump moves under the fork

These break the build rather than a build tag, so `go build std` for cosmo is
not enough on its own — build every port the fork supports.

- `decoderune` takes and returns a `uint` index.
- The ssa generator's `regMask` is a struct (`v1`, `v2`) with `union`/`addReg`
  methods, not a scalar. Do not hand-merge `opGen.go`; run
  `go run -C=_gen .` in `cmd/compile/internal/ssa` and commit what it writes.
- `internal/runtime/atomic` exports its linknames from `linkname.go` under
  `//go:linknamestd`, and a second `//go:linkname` for the same name is now a
  compile error.
- `gp.m.locks` is scaled by `mutexMLocksDelta` for mutexes, so an M can tell
  it is releasing its last mutex even with preemption disabled for other
  reasons. Every lock implementation must use it.

## The linker's cross-package reference check

go1.27's `cmd/link` rejects a reference from one std package's assembly to
another package's symbol unless the DEFINING object carries a linkname push.
Two shapes bit the cosmo port:

1. **A Go var shadowed by an assembly definition.** `runtime.__hostos` and
   `runtime.__syslib` had a Go `var` carrying `//go:linkname` AND a
   `DATA`/`GLOBL` pair in the rt0 assembly. The assembly definition is what
   the linker resolves, so the push never reached it. Fix: delete the assembly
   definitions and let the Go declarations own the symbols. The rt0 stubs
   still write them.
2. **Assembly-only helpers.** `cosmoLibcCallVariadic1`,
   `cosmo_xlat_errno_r0` and `cosmo_xlat_oflags_r2` exist only in
   `sys_cosmo_arm64.s`. The check's own comment names the remedy: it looks for
   a linkname on the symbol's ABI wrapper, which a Go declaration creates. The
   two translation helpers take their arguments in registers and are not
   callable from Go; their declarations say so, and exist only to carry the
   push.

`grep -rhoE 'runtime·[A-Za-z0-9_]+' --include='*.s' src/syscall
src/internal/runtime/syscall` lists every symbol subject to this check.

## What was verified

- `make.bash` on linux/amd64.
- `GOOS=cosmo go build std` for amd64 and arm64.
- `go build std` for js/wasm and wasip1/wasm, each also under `GOWASM=threads`.
- `go test -short go/build cmd/internal/moddeps`.
- A fat APE of `testdata/fizzbuzz` and of `testdata/runtimeprobe`, both with
  their `.dbg` and `.aarch64.elf` sidecars, executed on linux/amd64.
- The full `testdata/ape/apetest` suite against both binaries.
