# apeld: the native APE loaders the linker embeds

One loader per platform, each a single C file. A loader takes the APE's path, boots the payload from memory, and writes nothing to disk. `docs/APE-BOOT.md` describes how an APE finds one.

Upstream is [wow-look-at-my/ape-research](https://github.com/wow-look-at-my/ape-research). Its `LOG.txt` records the measurements behind each link pipeline.

| loader | how it boots the payload |
|---|---|
| `bin/apeld-linux-amd64`, `bin/apeld-linux-arm64` | copies the APE into a memfd, writes the payload's ELF header over offset 0, `execveat`s the memfd |
| `bin/apeld-darwin-arm64` | maps the arm64 ELF payload itself, builds the SysV stack and auxv, hands over a Syslib table of libSystem entry points, jumps |

windows/amd64 has no loader. The APE is a valid PE and the OS maps the payload straight from the file, read-only path or not.

darwin/amd64 is not a platform this toolchain emits. XNU reads the Mach-O header at offset 0, which an APE cannot carry there. The only route for that platform was a copy of the whole program.

## Rebuilding

```sh
./build.sh
```

`build.sh` needs `zig` (0.16.0 in upstream CI), `ld64.lld` and `llvm-strip`. `ZIG`, `LLD` and `STRIP` name them. The binaries are byte-reproducible. `TestApeLoaderBinaries` pins each one's SHA-256, so a rebuild that changes a byte fails the linker's own tests until the pin is updated with it.
