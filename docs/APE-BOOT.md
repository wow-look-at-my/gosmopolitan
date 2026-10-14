# How an APE starts without writing anything

The kernel cannot exec an APE as it stands. The file starts with `MZqFpD='`. That is neither an ELF header nor a Mach-O one. Windows is the exception. There the file is a valid PE, and the OS maps the payload straight from it, read-only path or not.

Everywhere else the shell runs, and it hands the file to a native loader. The loader opens the APE where it lies. It finds the payload for this machine on a 64 KiB boundary. Then it boots that payload from memory. The APE is never copied and never modified.

On Linux the loader maps each of the payload's PT_LOAD segments straight from the APE, `MAP_PRIVATE` at its own file offset. The pages come from the page cache, so every process that runs the same APE shares one copy. Nothing is copied into anonymous memory or a memfd. The loader then builds the payload's stack and jumps to its entry point. Data appended past the payloads is outside every segment. The payload reads it through the program's own path.

**A zero-write start needs a loader the host already reaches.** Putting one there takes root, one time. Either root installs the loader file, or root registers the `binfmt_misc` entry. Read the rest of this page with that in mind. A read-only path starts the program only on a machine where one of those is already done.

<<<<<<< HEAD
The privilege buys the machine its loader. It is not what runs the program. `mmap` needs no privilege at all. Neither does the loader once it runs.
=======
The privilege buys the machine its loader. It is not what runs the program. The loader maps the payload's segments from the APE file with no privilege at all. A mount can refuse an executable mapping. The loader then copies the APE into a memfd and execs that. `memfd_create` and `execveat` need no privilege either.
>>>>>>> origin/master

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

`dats/test/readonly-boot.dats` holds the standing read-only claim for linux and darwin. `dats/test/readonly-boot-container.dats` adds the `docker run --read-only` shape, which needs mount namespaces and so runs on linux alone. `dats/test/nt.dats` holds the claim for NT. The test job runs all three on every push. Each case makes the program's directory, the unpack directory and the loader's directory read-only. Then it writes a canary file there and requires that write to fail. A case that finds a writable directory fails rather than reporting a pass it did not earn.

The register file lives in procfs, not on the disk, so a read-only disk does not stop it. Root does. The registration is best effort: a run that cannot take it says nothing and goes through the search instead.

An image that must start APEs with nothing writable bakes the registration in at build time. That is one entry for the whole machine, not a file per program.

## The embedded loader, in RAM

A host that has none of them puts the loader the APE carries into the first of these it can write:

```
${APE_LOADERDIR:-/dev/shm /tmp "${o%/*}"}
```

`/dev/shm` is tmpfs, so those bytes live in RAM and reach no disk at all. `/tmp` follows, for a host that has no `/dev/shm`. Every darwin host is one of those. The program's own directory is last, for a read-only container that leaves nothing else: docker mounts `/dev/shm` **noexec**, and `--read-only` closes `/tmp`. `APE_LOADERDIR` replaces the list, and it may name more than one directory.

That last candidate is not a sidecar. `-u` unlinks the file before the program starts. Nothing is distributed. Nothing is left.

<<<<<<< HEAD
A container with nothing writable at all reaches none of them. The loader maps the payload from the APE where it lies, so a read-only mount does not stop the payload. A `noexec` mount does: the kernel refuses an executable mapping of a file there. So the loader itself still has to start from somewhere. Register the `binfmt_misc` entry on the host. The kernel then starts the APE with no file anywhere.
=======
A container with nothing writable at all reaches none of them. `memfd` is immune to both `noexec` and a read-only mount. The loader falls back to it when the APE's own mount refuses an executable mapping. No POSIX shell can create one. So the loader itself still has to start from somewhere. Register the `binfmt_misc` entry on the host.

The kernel then starts the APE with no file anywhere.
>>>>>>> origin/master

**The loader removes its own copy before the program starts.** The script execs it as `apeld -u <ape>`. `-u` makes it unlink `argv[0]` as its first act. The loader has to do this. A script cannot delete anything after it execs.

So nothing is left for anybody to find. On linux nothing was written to a disk in the first place. The name carries the PID. The mode is 700. No run can therefore read or collide with another's.

A loader somebody installed is called without `-u` and stays where it is. That is why the flag rides argv. An environment variable reaches the payload. A nested run can then delete the installed copy.

Unlinking the file first and exec'ing it through `/dev/fd` works on linux and is shorter. XNU answers that with `EACCES`. It is therefore not available on darwin, and both platforms take the same path instead.

The loader is many bytes on linux/amd64, against the program's megabytes. `dd` reads it out of the APE. The darwin loader is gzipped. It goes through `gzip -dc` as well.

Root also hands the loader to `binfmt_misc` on the way past, before the unlink. Every later run on that machine then skips this path entirely. `APE_NOBINFMT` marks a pass that took it, so a kernel that hands the file back to a shell cannot make a loop.

A **noexec** directory takes the loader fine, and execve then refuses it. The loop treats that like any other failure and tries the next directory.

A host where no directory in the list can be written refuses. It names the loader it wants and how to supply one.

A host with no loader and nowhere to put one refuses to start. It names the loader it wants:

```
APE: no apeld-linux-amd64 on this host, and nowhere to unpack the embedded one: install it on PATH, or point APE_LOADER at it
```

## What it costs the program

<<<<<<< HEAD
Only this payload's PT_LOAD ranges are mapped. The other payload and any data past the load span are never touched. The page cache shares the mapped pages across every process running the APE, and only a page a process writes becomes its own. The runtimeprobe `filemap` check asserts the probe's text is a mapping of the APE file itself.

On Linux `/proc/self/exe` names the loader, because the kernel exec'd the loader and not the APE. The loader sets `AT_EXECFN` to the APE's path. `os.Executable` returns that path when `/proc/self/exe` names a different file. cgo's `cosmo_find_progname` does the same for libcosmo. A program that re-execs itself, or reads its own file, gets the APE. `/proc/self/maps` names the APE for every segment.

Anything that reads `/proc/self/exe` directly still sees the loader. The loader sets the process's `comm` to the APE's basename, so `ps` and `top` show the program's name.
=======
On Linux the loader maps the payload itself, so `/proc/self/exe` names the loader. The loader passes the APE's open descriptor as `AT_EXECFD`, and `os.Executable` reads the path from `/proc/self/fd/<n>`. A program that re-execs itself, or reads its own file, gets an openable path. `/proc/self/maps` names the APE.

The memfd fallback leaves `/proc/self/exe` reading `/memfd:<name>`. `os.Executable` recognises that and resolves `argv[0]`, which the loader sets to the APE's own path. The memfd holds only this payload's PT_LOAD ranges, at their APE offsets. The rest of it is a hole, which takes no memory.

Anything that reads `/proc/self/exe` directly sees the loader, or the memfd name.

Each start of the memfd path copies the payload's load span. For a toolchain binary of 868 MB a whole-file copy was 0.45 s per process. Mapping costs 0.03 s.
>>>>>>> origin/master

On macOS nothing changes. The loader maps the payload itself, and `os.Executable()` resolves `argv[0]`.

## Nothing copies the program

No host stages a copy of the binary on disk, and no host writes a header into one. Only the Linux noexec fallback copies it, and only into memory. `cosmoape`'s platform table is exactly the set that starts without doing either.

darwin/amd64 is absent from that table. Intel macs are out of support. XNU reads the Mach-O header at offset 0, which an APE cannot carry there. A copy was that platform's only route.

TMPDIR and HOME are not read anywhere in the script. Both are caller-supplied, and neither can be trusted. TMPDIR can be unset, empty, or point at something unwritable. A container run as a bare numeric UID still gets `HOME="/"`.
