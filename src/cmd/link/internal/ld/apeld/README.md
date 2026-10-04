# apeld: the native APE loaders the linker embeds

One loader per platform, each a single C file. A loader takes the APE's path, boots the payload from memory, and writes nothing to disk. `docs/APE-BOOT.md` describes how an APE finds one.

Upstream is [wow-look-at-my/ape-research](https://github.com/wow-look-at-my/ape-research). Its `LOG.txt` records the measurements behind each link pipeline.

| loader | how it boots the payload |
|---|---|
| `bin/apeld-linux-amd64`, `bin/apeld-linux-arm64` | copies the APE into a memfd, writes the payload's ELF header over offset 0, `execveat`s the memfd |
| `bin/apeld-darwin-arm64` | maps the arm64 ELF payload itself, builds the SysV stack and auxv, hands over a Syslib table of libSystem entry points, jumps |

windows/amd64 has no loader. The APE is a valid PE and the OS maps the payload straight from the file, read-only path or not.

darwin/amd64 is not a platform this toolchain emits. XNU reads the Mach-O header at offset 0, which an APE cannot carry there. The only route for that platform was a copy of the whole program.

## Building

Git does not track `bin/`. `make.bash` and `make.bat` compile the loaders into it before anything builds cmd/link: see `src/cmd/dist/apeld.go`. The build needs these on `PATH`, and fails naming any that is missing:

| tool | where it comes from |
|---|---|
| `zig` 0.16.0 | https://ziglang.org/download/ |
| `ld64.lld` and `llvm-strip` from LLVM 18.1.8 | `lld-18` and `llvm-18` from apt.llvm.org (`/usr/lib/llvm-18/bin`), `brew install llvm@18`, or `LLVM-18.1.8-win64.exe` |

dist refuses any other zig or LLD. ld64.lld writes its own version into the darwin loader, so another LLD makes other bytes. `TestApeLoaderBinariesMatchTheirPins` pins each loader's SHA-256, so a source change that moves a byte updates its pin in the same commit.
