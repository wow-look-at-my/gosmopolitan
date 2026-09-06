# How an APE runs without writing to itself

The kernel cannot exec an APE as it stands. The file starts with `MZqFpD='`, which is neither an ELF header nor a Mach-O one. Something has to put a real header over those first bytes.

For years that something wrote the header into the file it was running. That needs the file to be writable, it changes the file's checksum, and it costs a fat APE every platform but the one that. A read-only path -- a sandbox mount, a package directory, a CI cache -- fails outright.

The bootstrap script now stages a COPY and corrects the copy.

## The staged copy

```
/tmp/.ape-run-1-<uid>/<file identity>/<basename>
```

No environment variable is read to find this path. TMPDIR and HOME are both caller-supplied, and neither can be trusted: TMPDIR can be unset, empty, or pointed at something unwritable, and a container. `/tmp` is reliably world-writable (mode 1777) on virtually every host this binary runs on, so staging goes there unconditionally.

`<uid>` is `id -u` -- a syscall, not an environment variable -- and stands in for the per-user isolation a real HOME can otherwise give this path.

The file identity is `device.inode.mtime.size`, read with `stat -c %d.%i.%.9Y.%s` on GNU and `stat -f %d.%i.%Fm.%z` on BSD. A host with no `stat` falls back to `cksum` over the contents.

The mtime is read to the NANOSECOND. Seconds are not enough: a rebuild that lands within one second of the last one, in place and at the same size, keys to. A build loop produces exactly that, and the failure is silent.

The copy is published with `mv -f` from a `$$`-suffixed temporary, so a second process starting at the same moment either sees no copy. A later run costs one `stat` and one `exec`.

The copy keeps the original's basename, so `${0##*/}` -- what a usage line prints -- stays right either way.

## Two things staging tries on the host

Both need root. Both fail silently and change nothing when they do not land. Neither is retried once a copy exists.

**binfmt_misc.** The script registers the APE magic against `/bin/sh`:

```
:APE:M::MZqFpD='::/bin/sh:
```

That is the kernel doing what a shell already does on ENOEXEC. With it registered, a caller that `execve`s the file directly -- Go's `os/exec`, a build system, a test harness -- stops needing a shell in.

The registration is written with a DOUBLE-quoted `printf`. The macOS ARM64 loader decodes every `printf '` in the first 8192 bytes as a candidate boot header, and this string is not one. `TestFatBootHeaders` holds that count at two for a fat APE. The redirect sits inside a `{ ...; } 2>/dev/null` group, because a shell reports a redirect it cannot open on its own stderr -- outside the group, every.

**A bind mount.** Staging records whether this host can `unshare -m`. A run that finds that mark binds the staged copy over the APE's own path inside a private mount namespace and execs it there. `argv[0]`, `/proc/self/exe`, and anything the program resolves next to itself then point where the caller put them, not into the run directory.

The bind is taken only when the process is ALREADY root. Reaching a mount namespace through a user namespace instead can have the program see itself as uid 0, which is a stranger surprise than.

Nothing outside that namespace sees the mount. While it is live, the owner of the file can still overwrite it in place, move it, delete it, and `rm -rf` the directory holding. `testdata/ape/apetest` covers the shape of the script. The mount behaviour is a property of private namespaces, not of this code.

A mount that does not take falls through to exec'ing the staged copy, so the program runs either way.

## What it costs

Measured with `hyperfine` on a 3.5 MB binary:

| path | mean |
|---|---|
| the staged copy, exec'd directly | 1.3 ms |
| through the script, copy already staged | 5.5 ms |
| the same, with the bind mount | 9.0 ms |

The first run adds the copy itself: 2.1 ms at 3.5 MB, 5.9 ms at 24 MB.
