# How an APE starts without writing anything

The kernel cannot exec an APE as it stands. The file starts with `MZqFpD='`. That is neither an ELF header nor a Mach-O one. Windows is the exception. There the file is a valid PE, and the OS maps the payload straight from it, read-only path or not.

Everywhere else the shell runs, and it hands the file to a native loader. The loader opens the APE where it lies. It finds the payload for this machine on a 64 KiB boundary. Then it boots that payload from memory. The APE is never copied and never modified. A read-only path therefore starts the program like any other path. That covers a sandbox mount, a package directory, a CI cache and a read-only root.

The loaders live in `src/cmd/link/internal/ld/apeld`. That directory's README says what each one does. They come from [ape-research](https://github.com/wow-look-at-my/ape-research), whose `LOG.txt` carries the measurements.

## Finding a loader

The bootstrap script takes the first of these it can execute. It writes nothing to get there.

1. `$APE_LOADER`.
2. `.apeld-<os>-<arch>` beside the binary. That is how a distributor ships one on read-only media.
3. `/usr/local/lib/ape/apeld-<os>-<arch>`, then `/usr/lib/ape/apeld-<os>-<arch>`.
4. `apeld-<os>-<arch>`, `apeld`, then `ape` on `PATH`. `ape` is the cosmo loader. It reads the boot ELF headers the script still carries as `printf` statements. That is the only reason those statements are still there.

A host that has any of these needs nothing writable at all. The toolchain carries the same binaries, so installing one is a copy:

```sh
l=apeld-$(go env GOHOSTOS)-$(go env GOHOSTARCH)
sudo install -D -m755 "$(go env GOROOT)/src/cmd/link/internal/ld/apeld/bin/$l" "/usr/local/lib/ape/$l"
```

Do that once on an image, and every APE that image ever runs starts without writing.

## Unpacking the embedded loader

A host that has none of them unpacks the loader the APE carries:

```
${APE_LOADERDIR:-/tmp/.ape-ld-1-<uid>}/apeld-<os>-<arch>-<tag>
```

The tag is the first four bytes of the loader's own SHA-256. A toolchain that ships a different loader therefore unpacks to a path of its own.

This is not the staged copy it replaces. The loader is 816 bytes on linux/amd64, against the program's megabytes. It is the same file for every APE of that architecture. One unpack serves the whole machine for good. `dd` reads it out of the APE. The darwin loader is gzipped. It goes through `gzip -dc` as well.

`APE_LOADERDIR` moves that directory. It exists for the case `/tmp` cannot serve. A **noexec** mount takes the loader fine, and execve then refuses it. Nothing validates the value, because `mkdir` and `dd` already fail loudly.

`<uid>` is `id -u`. That is a syscall, not an environment variable. It stands in for the per-user isolation a real HOME can otherwise give this path.

A host with no loader and nowhere to put one refuses to start. It names the loader it wants:

```
APE: no apeld-linux-amd64 on this host, and nowhere to unpack the embedded one: install it on PATH, or point APE_LOADER at it
```

## What it costs the program

`os.Executable()` on Linux reads `/memfd:<name>`, because the loader execs a memfd. `argv[0]` is the path the caller used. A program that re-execs itself by `os.Executable()` must read `argv[0]` instead.

On macOS nothing changes. The loader maps the payload itself, and `os.Executable()` resolves `argv[0]`.

## darwin/amd64 still stages a copy

That platform has no loader yet. It is also absent from `cosmoape.Default()`. Its syscall and signal surfaces are implemented. No Intel-mac runner exists. Nothing there has ever been executed.

XNU reads the Mach-O header at offset 0. A build that selects this platform therefore copies itself to

```
${APE_RUNDIR:-/tmp}/.ape-run-1-<uid>/<file identity>/<basename>
```

It then moves the header into place on the copy with `dd`. The APE itself is still never written.

TMPDIR and HOME are not read. Both are caller-supplied, and neither can be trusted. TMPDIR can be unset, empty, or point at something unwritable. A container run as a bare numeric UID still gets `HOME="/"`. `/tmp` is world-writable on virtually every host this binary runs on.

The file identity is `device.inode.mtime.size`. `stat -L -f %d.%i.%Fm.%z` reads it on BSD, and `stat -L -c %d.%i.%.9Y.%s` reads it on GNU. `cksum` over the contents stands in where neither exists.

The mtime is read to the nanosecond. A build loop rewrites the file in place inside one second, at the same size. Seconds alone therefore key that rebuild to the copy already staged.
