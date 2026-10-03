#!/bin/sh
# Builds every loader into bin/ with zig cc. ZIG names the compiler
# (default: zig on PATH). Each build is static; the linker's own tests
# assert that, and pin the resulting bytes.
set -eu
cd "$(dirname "$0")"
ZIG=${ZIG:-zig}
mkdir -p bin

COMMON="-Os -fno-stack-protector -fno-unwind-tables -fno-asynchronous-unwind-tables -Wall"

# The linker script packs everything into one PT_LOAD; the section header
# table is then stripped, since a static executable does not need it.
for t in x86_64:amd64 aarch64:arm64; do
	$ZIG cc -target "${t%%:*}-linux-musl" $COMMON -nostdlib -ffreestanding -static -fno-pic -fno-pie \
		-Wl,--gc-sections -Wl,--build-id=none -Wl,-z,norelro -Wl,-T,linux/apeld.ld -s \
		-o "bin/apeld-linux-${t##*:}" linux/apeld.c
	${STRIP:-llvm-strip} --strip-sections "bin/apeld-linux-${t##*:}"
done

# zig compiles the object; ld64.lld links it, because zig's own Mach-O linker
# cannot merge __DATA_CONST into __DATA, and each segment costs a 16K page.
# libSystem.tbd comes from zig's bundled darwin libc stubs. The ad-hoc
# signature's identifier is the output basename, so the name is fixed.
# `zig env` prints ZON (.lib_dir = "...") from 0.16 and JSON ("lib_dir": "...")
# before it. Read both, and fail loudly rather than guess: a wrong -L makes
# ld64.lld report every libSystem symbol as undefined, which reads as a
# source problem.
ZIGLIB=${ZIGLIB:-$($ZIG env 2>/dev/null | sed -n 's/^[[:space:]]*[.",]*lib_dir[",]*[[:space:]]*[:=][[:space:]]*"\(.*\)".*/\1/p' | head -1)}
[ -n "$ZIGLIB" ] && [ -d "$ZIGLIB/libc/darwin" ] || {
	echo "build.sh: cannot find zig's darwin libc stubs under lib_dir ('$ZIGLIB'); set ZIGLIB" >&2
	exit 1
}
$ZIG cc -target aarch64-macos $COMMON -c -o bin/apeld-darwin.o darwin/apeld.c
${LLD:-ld64.lld} -arch arm64 -platform_version macos 12.0 12.0 -L"$ZIGLIB/libc/darwin" -lSystem \
	-dead_strip -S -x -no_uuid -no_function_starts -no_data_const -fixup_chains \
	-o bin/apeld-darwin-arm64 bin/apeld-darwin.o
rm -f bin/apeld-darwin.o

ls -l bin
