# cgo on GOOS=cosmo

A cgo program builds as the normal fat APE. Each payload carries C code that the cosmocc compiler for its own architecture compiled, linked against that architecture's libcosmo.

```sh
export PATH="/opt/cosmocc/bin:$PATH"   # cosmocc 4.0.2 is the tested release
GOOS=cosmo go build -o prog.com .      # cgo is on, both payloads carry C
```

`dats/checks/cosmo-cgo.dats` is the proof on the build host. It builds `testdata/cgoprobe` fat, runs it on a linux/amd64 host, and runs its arm64 payload under qemu-aarch64 through cosmocc's `ape-aarch64.elf` loader. The probe covers a plain call, printf, errno and a Go callback. It also calls Go from a thread that C started, and runs many goroutines in and out of C under GC.

The Linux build leg also builds the probe and hands it to the test job. `TestCgoProbe` in `testdata/ape/apetest` runs it on each test host: ubuntu amd64, macOS arm64 and Windows amd64. It asserts every check and the host's own architecture.

## Compilers

| GOARCH | default CC | override |
|---|---|---|
| amd64 | `x86_64-unknown-cosmo-cc` | `CC_FOR_cosmo_amd64`, `CXX_FOR_cosmo_amd64` |
| arm64 | `aarch64-unknown-cosmo-cc` | `CC_FOR_cosmo_arm64`, `CXX_FOR_cosmo_arm64` |

- cgo is on by default when the compiler for GOARCH is on PATH (`cmd/go/internal/cfg/cosmocc.go`, `go/build/cosmocc.go`). Without it cgo stays off, as before.
- `CGO_ENABLED=1` with no compiler fails and names it: `cgo: C compiler "x86_64-unknown-cosmo-cc" not found`.
- A plain `CC` applies to GOARCH only. The fat build's sibling gets `CC_FOR_cosmo_<sibling>` or the default, and the parent's cgo setting (`cosmoSiblingCgoEnv` in `cmd/go/internal/work/cosmofat.go`). A sibling without its compiler fails by name.
- A cosmo build always sets the `netgo` and `osusergo` tags. cosmo satisfies the `linux` tag, so their cgo files will otherwise call libcosmo. Then every program that imports `net` will stop on NT.

## Link

cgo forces an external link (`internal/platform.MustLinkExternal`). `cmd/link` runs the raw `<arch>-linux-cosmo-gcc` next to the compiler, not the cosmocc driver. The driver always adds cosmocc's APE script and APE head, and `cmd/link` writes the APE itself (`cosmolink.go`).

- The linker scripts are in `cmd/link/internal/ld/cosmolds.go`. They keep the section bodies of cosmocc's `ape.lds` and `aarch64.lds`, so libcosmo finds its init sections and symbols, and they name Go's sections.
- amd64 links at `0x400000`. libcosmo uses 32-bit absolute relocations. As a result, the image must sit below 4 GiB. A pure-Go link keeps `0x100000000`. The PE header reads the base from the image (`apeImageBase`).
- amd64 Go text sits before `_ereal`. At startup libcosmo rewrites its `gs:0x30` TLS loads between `_ereal` and `__privileged_start`, and Go code must stay out of that range.
- Go's `gs:0x28` references keep their fixed offset in an external link (`cosmoFixedTLS`). The ELF thread pointer belongs to libcosmo.
- arm64 keeps the 4 TiB base. cosmo arm64 code is PC-relative.
- The entry is libcosmo's `_start`. It starts the C runtime, then calls `main`, which is Go's `rt0_go`.
- `-Wl,--wrap=pthread_create` sends every thread through `runtime/cgo`'s wrapper.

## NT

The PE header of a cgo image takes its entry and its imports from libcosmo (`cosmoNTBoot` in `cosmolink.go`). A pure-Go image keeps `_rt0_cosmo_nt` and `runtime.ntidata`.

- AddressOfEntryPoint is libcosmo's `WinMain`. The script pulls it in with `EXTERN(WinMain)`. WinMain starts libcosmo on NT and calls `main`, as `_start` does on Unix.
- The import directory is libcosmo's. Each `__imp_` object carries its own `.idata.ro.*` descriptor, lookup and name sections and a `.piro.data.sort.iat.*` slot. The script bounds them with `ape_idata_idt`, `ape_idata_idtend`, `ape_idata_iat` and `ape_idata_iatend`, and writes the zero descriptor itself. Its relocations are RVAs against `0x400000`, the amd64 cgo base.
- `x_cgo_init` copies `__imp_GetProcAddress` and `__imp_LoadLibraryA` into `runtime.ntiat`. The Go NT layer resolves everything else through those two, as on the pure-Go path.
- WinMain passes `main` no auxv, so `sysargs` takes a 4 KiB page on NT. `startupRand` stays empty and `randinit` reads `ProcessPrng`.
- Threads come from libcosmo's `pthread_create`, which calls `CreateThread`.

## Thread state

libcosmo owns the C thread pointer. Go keeps `g` apart from it (`runtime/cgo/gcc_cosmo.c`).

| | libcosmo reads | Go reads g at | set up by |
|---|---|---|---|
| amd64 Linux | `%fs:0` | `gs:0x28` | GS base 0x28 below a `__thread` slot pair |
| amd64 XNU | `gs:0x30` | `gs:0x28` | the same pair, slot 1 holds the TIB |
| amd64 NT | a TEB TLS slot | `gs:0x28` | nothing: it is the TEB's ArbitraryUserPointer |
| arm64 Linux | `x28` | `TPIDR_EL0` + `tls_g` | `TPIDR_EL0` = the thread's `x28` |
| arm64 XNU | `x28` | an Apple TSD slot off `TPIDRRO_EL0` | a second TSD slot holds the thread's `x28` |

- `x_cgo_init` binds the main thread and copies `__hostos` and `__syslib` from libcosmo, because the APE boot hands them to libcosmo's `_start`.
- The pthread_create wrapper binds every other thread before its start routine runs. A thread that C starts therefore reads a nil `g`, and `needm` gives it an M.
- arm64 `asmcgocall` loads `x28` through `runtime.cosmoCTP` before it calls C and restores `g` after. `main` saves libcosmo's `x28` in `cosmoMainTP` before `rt0_go` reuses R28, and the call to `x_cgo_init` uses it.
- XNU writes `TPIDR_EL0` at every context switch: it carries the CPU number. So on macOS `x_cgo_init` makes Apple pthread keys through the Syslib's `dlsym`, as upstream's darwin `tlsinit` does. It stores their offsets in `cosmoHostSlots`, which `load_g`, `save_g` and `cosmoCTP` read.
- `runtime/cgo` leaves out its mmap and sigaction hooks on cosmo, as the runtime already does.

## Not done

The NT and macOS rows are also in `docs/STUBS-INVENTORY.md`.

- Wine cannot run any cosmocc program, cgo or not: libcosmo's startup retries one fixed `MapViewOfFileEx` forever there. The windows test leg is the NT proof.
- macOS Intel: nothing has run there. The amd64 path writes the GS base with `thread_fast_set_cthread_self`.
- C's buffered stdio is not flushed when Go exits. That matches upstream cgo: C code calls `fflush`.
- Only `-buildmode=exe`. No internal link, c-archive, c-shared or plugin.
