# How an APE starts without writing anything

The kernel cannot exec an APE as it stands. The file starts with `MZqFpD='`. That is neither an ELF header nor a Mach-O one. Windows is the exception. There the file is a valid PE, and the OS maps the payload straight from it, read-only path or not.

Everywhere else the shell runs, and it hands the file to a native loader. The loader opens the APE where it lies. It finds the payload for this machine on a 64 KiB boundary. Then it boots that payload from memory. The APE is never copied and never modified.

**A zero-write start needs a loader the host already reaches.** Putting one there takes root, one time. Either root installs the loader file, or root registers the `binfmt_misc` entry. Read the rest of this page with that in mind. A read-only path starts the program only on a machine where one of those is already done.

The privilege buys the machine its loader. It is not what runs the program. `memfd_create` and `execveat` need no privilege at all. Neither does the loader once it runs.

An ordinary user on a machine with neither still starts the program. That run unpacks the loader. The file then stays. So an unprepared host offers one small file or a refusal. It never offers a silent zero-write start.

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

## The embedded loader, in RAM

A host that has none of them puts the loader the APE carries into the first of these it can write:

```
${APE_LOADERDIR:-/dev/shm /tmp "${o%/*}"}
```

`/dev/shm` is tmpfs, so those bytes live in RAM and reach no disk at all. `/tmp` follows, for a host that has no `/dev/shm`. Every darwin host is one of those. The program's own directory is last, for a read-only container that leaves nothing else: docker mounts `/dev/shm` **noexec**, and `--read-only` closes `/tmp`. `APE_LOADERDIR` replaces the list, and it may name more than one directory.

That last candidate is not a sidecar. `-u` unlinks the file before the program starts. Nothing is distributed. Nothing is left.

A container with nothing writable at all reaches none of them. `memfd` is immune to both `noexec` and a read-only mount, and the loader uses it for the payload. No POSIX shell can create one. So the loader itself still has to start from somewhere. Register the `binfmt_misc` entry on the host. The kernel then starts the APE with no file anywhere.

**The loader removes its own copy before the program starts.** The script execs it as `apeld -u <ape>`. `-u` makes it unlink `argv[0]` as its first act. The loader has to do this. A script cannot delete anything after it execs.

So nothing is left for anybody to find. On linux nothing was written to a disk in the first place. The name carries the PID. The mode is 700. No run can therefore read or collide with another's.

A loader somebody installed is called without `-u` and stays where it is. That is why the flag rides argv. An environment variable reaches the payload. A nested run can then delete the installed copy.

Unlinking the file first and exec'ing it through `/dev/fd` works on linux and is shorter. XNU answers that with `EACCES`. It is therefore not available on darwin, and both platforms take the same path instead.

The loader is 892 bytes on linux/amd64, against the program's megabytes. `dd` reads it out of the APE. The darwin loader is gzipped. It goes through `gzip -dc` as well.

Root also hands the loader to `binfmt_misc` on the way past, before the unlink. Every later run on that machine then skips this path entirely. `APE_NOBINFMT` marks a pass that took it, so a kernel that hands the file back to a shell cannot make a loop.

A **noexec** directory takes the loader fine, and execve then refuses it. The loop treats that like any other failure and tries the next directory.

A host where no directory in the list can be written refuses. It names the loader it wants and how to supply one.

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
