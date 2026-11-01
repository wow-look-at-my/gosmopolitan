# inotify on every host

A cosmo binary makes the Linux inotify calls on Linux, macOS and Windows, and sees the same records on each. That is why fsnotify's inotify backend runs unchanged and a program needs no host check.

## How it is served

`src/syscall/inotify_core_cosmo.go` owns the instance on every host, Linux included. The descriptor a caller holds is one end of a socket pair, so the runtime poller waits on it the same way everywhere. A goroutine writes records to the other end.

Each host supplies only hints, through `inotifySource`. A hint says a name appeared, left or may changed. `src/syscall/inotify_events_cosmo.go` then compares a stat with the last stat of that name, and that comparison decides the records. A host therefore has no way to report something another host will not.

| Host | Source | File |
|---|---|---|
| Linux | kernel inotify | `src/syscall/inotify_kernel_cosmo.go` |
| macOS | FSEvents | `src/syscall/inotify_fsevents_cosmo_arm64.go`, `src/runtime/fsevents_cosmo_arm64.go` |
| Windows | `ReadDirectoryChangesW` | `src/syscall/inotify_nt_cosmo.go` |

No host costs a descriptor per child of a watched directory.

A macOS host calls CoreServices through the APE loader's `dlopen`. FSEvents delivers from a dispatch queue, so a C callback writes each event to a pipe and a goroutine reads it. An FSEvents stream covers everything under its directory. By name, events about other directories are dropped. A stream watches a path, so a rename restarts it and compares the directory with what is recorded of it.

## What a caller sees

`IN_ACCESS`, `IN_OPEN` and `IN_CLOSE_NOWRITE` are accepted and never fire. Neither macOS nor NT tells an unprivileged process about them. A Linux host can report them and does not, because a program that relied on them will then run on one host only.

A write is reported when the descriptor that made it closes, as `IN_MODIFY` then `IN_CLOSE_WRITE`. Neither macOS nor NTFS reports a write sooner, and `fsync` does not change that. A Linux host holds the kernel's `IN_MODIFY` back until the close.

A truncate by path, a new mode, a new owner and a new time are reported when they happen. A file that shrinks through an open descriptor is reported when it happens too.

A rename seen at both ends is `IN_MOVED_FROM` then `IN_MOVED_TO`, with one cookie. A name arriving from an unwatched directory is `IN_CREATE`. A name leaving for one is `IN_DELETE`. NT cannot tell either from a real create or delete, so no host reports them as moves. A deletion that may be half of a rename waits about 30ms for the other half.

A name that arrives with content reports `IN_CREATE`, `IN_MODIFY`, `IN_CLOSE_WRITE`. A name created and deleted before a host looks reports `IN_CREATE` then `IN_DELETE`.

A name replaced by an arrival nobody saw leave reports `IN_DELETE` then `IN_CREATE`.

A watched name that leaves unpaired reports `IN_DELETE_SELF` then `IN_IGNORED`.

`IN_DONT_FOLLOW` on a symlink watches the link, through its parent directory. It never answers ELOOP.

A read returns whole records. It answers EINVAL when the next record does not fit the buffer.

## Where hosts still differ

A file that grows through a descriptor that stays open is one such case. macOS reports it when it happens. Linux reports it at the close, because a kernel `IN_MODIFY` cannot tell that from a write.

A write through a descriptor opened before the watch began is reported at once on Linux, which saw no open to wait for. A read-only descriptor held open on Linux also delays a growing truncate by path to its close.

`dup` of an instance descriptor gives a second name for the socket. A read through it gets raw socket bytes, not whole records, and it never answers EINVAL.

macOS on Intel has no inotify, because no APE starts on that host. See the macOS Intel status in docs/PLATFORM-STATUS.md.

## Coverage

`src/syscall/inotify_cosmo_test.go` runs on the Linux host. The runtimeprobe `inotify` check, `testdata/runtimeprobe/inotify_cosmo.go` and `inotify_contract_cosmo.go`, runs the same contract on all hosts and is mandatory on each.
