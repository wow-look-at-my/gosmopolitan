# How an APE starts without writing anything

The kernel cannot exec an APE as it stands. The file starts with `MZqFpD='`. That is neither an ELF header nor a Mach-O one. Windows is the exception. There the file is a valid PE, and the OS maps the payload straight from it, read-only path or not.

Everywhere else the shell runs, and it hands the file to a native loader. The loader opens the APE where it lies. It finds the payload for this machine on a 64 KiB boundary. Then it boots that payload from memory. The APE is never copied and never modified. A read-only path therefore starts the program like any other path. That covers a sandbox mount, a package directory, a CI cache and a read-only root.

The loaders live in `src/cmd/link/internal/ld/apeld`. That directory's README says what each one does. They come from [ape-research](https://github.com/wow-look-at-my/ape-research), whose `LOG.txt` carries the measurements.

## Finding a loader

The bootstrap script takes the first of these it can execute. It writes nothing to get there.

1. `$APE_LOADER`.
2. `/usr/local/lib/ape/apeld-<os>-<arch>`, then `/usr/lib/ape/apeld-<os>-<arch>`.
3. `apeld-<os>-<arch>`, `apeld`, then `ape` on `PATH`. `ape` is the cosmo loader. It reads the boot ELF headers the script still carries as `printf` statements. That is the only reason those statements are still there.

Nothing looks beside the binary. An APE is one file. A loader shipped next to it is a second thing to carry, which is the property an APE exists to avoid.

A host that has any of these needs nothing writable at all. The toolchain carries the same binaries, so installing one is a copy:

```sh
l=apeld-$(go env GOHOSTOS)-$(go env GOHOSTARCH)
sudo install -D -m755 "$(go env GOROOT)/src/cmd/link/internal/ld/apeld/bin/$l" "/usr/local/lib/ape/$l"
```

Do that once on an image, and every APE that image ever runs starts without writing.

## Handing the loader to the kernel

Once the script has a loader it registers it with `binfmt_misc`, under the APE magic, with the `F` flag:

```
:APE:M::MZqFpD='::<loader>:F
```

From then on `execve` of any APE on that machine starts it directly. No shell runs. Nothing is searched for. Nothing is read off the disk to find the loader. `os/exec`, a build system and a test harness all start an APE like any other program.

`F` is what makes this work on a read-only image. The kernel opens the interpreter at registration and keeps the descriptor. The loader then needs no path at all.

Measured: an entry registered with `F` still boots the payload after its interpreter file is deleted. `/tmp`, the program's directory and `/usr/local/lib` were all mounted read-only in a private mount namespace for that run.

Measured on a writable host with no loader installed. The first run leaves nothing at all under `/tmp`. The entry it registered names a path that the same run then deleted. An unprivileged run of the same program keeps its unpacked loader and registers no entry.

`dats/test/readonly-boot.dats` holds the standing read-only claim for linux and darwin, and `dats/test/nt.dats` holds it for NT. The test job runs all three on every push. Each case makes the program's directory, the unpack directory and the loader's directory read-only. Then it writes a canary file there and requires that write to fail. A case that finds a writable directory fails rather than reporting a pass it did not earn.

The register file lives in procfs, not on the disk, so a read-only disk does not stop it. Root does. The registration is best effort: a run that cannot take it says nothing and goes through the search instead.

An image that must start APEs with nothing writable bakes the registration in at build time. That is one entry for the whole machine, not a file per program.

## Unpacking the embedded loader

A host that has none of them unpacks the loader the APE carries:

```
${APE_LOADERDIR:-/tmp/.ape-ld-1-<uid>}/apeld-<os>-<arch>-<tag>
```

The tag is the first four bytes of the loader's own SHA-256. A toolchain that ships a different loader therefore unpacks to a path of its own.

This is not the staged copy it replaces. The loader is 816 bytes on linux/amd64, against the program's megabytes. It is the same file for every APE of that architecture. One unpack serves the whole machine for good. `dd` reads it out of the APE. The darwin loader is gzipped. It goes through `gzip -dc` as well.

A run that both unpacked the loader and registered it deletes the file at once. `F` already handed the kernel the descriptor. The program then starts with nothing on the disk to show for it. The unpacked file therefore survives only where registration is not available, which means an unprivileged run. `APE_NOBINFMT` in the environment marks a pass that took this path. A second pass reads it and execs the loader by name instead, which stops a loop.

`APE_LOADERDIR` moves that directory. It exists for the case `/tmp` cannot serve. A **noexec** mount takes the loader fine, and execve then refuses it. Nothing validates the value, because `mkdir` and `dd` already fail loudly.

`<uid>` is `id -u`. That is a syscall, not an environment variable. It stands in for the per-user isolation a real HOME can otherwise give this path.

A host with no loader and nowhere to put one refuses to start. It names the loader it wants:

```
APE: no apeld-linux-amd64 on this host, and nowhere to unpack the embedded one: install it on PATH, or point APE_LOADER at it
```

## What it costs the program

On Linux `/proc/self/exe` reads `/memfd:<name>`, because the loader execs a memfd. That is the anonymous file's name, not a path. `os.Executable` recognises it and resolves `argv[0]` instead, which the loader sets to the APE's own path. A program that re-execs itself, or reads its own file, gets an openable path.

Anything that reads `/proc/self/exe` directly still sees the memfd name. The same goes for `/proc/self/maps`.

On macOS nothing changes. The loader maps the payload itself, and `os.Executable()` resolves `argv[0]`.

## Nothing copies the program

No host stages a copy of the binary, and no host writes a header into one. `cosmoape`'s platform table is exactly the set that starts without doing either.

darwin/amd64 is absent from that table. Intel macs are out of support. XNU reads the Mach-O header at offset 0, which an APE cannot carry there. A copy was that platform's only route.

TMPDIR and HOME are not read anywhere in the script. Both are caller-supplied, and neither can be trusted. TMPDIR can be unset, empty, or point at something unwritable. A container run as a bare numeric UID still gets `HOME="/"`.
