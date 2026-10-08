# cgo on GOOS=cosmo. cgoprobe calls C that uses libcosmo's stdio and errno,
# calls back into Go, and calls Go from a thread that C started. It prints
# one line per check and exits 1 if any check fails.
#
# The cosmocc toolchain must sit in /opt/cosmocc, and qemu-aarch64 must be on
# PATH. The CI job installs both. The toolchain must already be built.
tests:
	- desc: a cgo program builds as a fat APE and runs here
	  cmd: export PATH="$PWD/bin:/opt/cosmocc/bin:$PATH"; out="$(mktemp -d)"; cd testdata/cgoprobe && go build -o "$out/cgoprobe.com" . && "$out/cgoprobe.com"
	  timeout: 10m
	  exit: 0
	  outputs:
		stdout:
			- ok add
			- "c printf: hello 42"
			- ok printf
			- ok errno
			- ok callback
			- ok thread
			- ok concurrent
			- ok dlerror
			- arch amd64
		!stdout:
			- FAIL

	# A cgo package's plain dlopen, dlsym and dlclose reach cosmo_dlopen, which loads a host library.
	- desc: plain dlopen in cgo code loads a host library and finds a symbol in it
	  cmd: export PATH="$PWD/bin:/opt/cosmocc/bin:$PATH"; out="$(mktemp -d)"; cd testdata/cgoprobe && GOCOSMOFAT=0 GOARCH=amd64 go build -o "$out/dlopen.com" . && "$out/dlopen.com" dlopen libm.so.6 cos
	  timeout: 10m
	  exit: 0
	  outputs:
		stdout:
			- ok dlerror
			- ok dlopen
		!stdout:
			- FAIL

	# -s reaches the host link as -Wl,-S, never as -s: the NT boot reads WinMain
	# out of the linked image's symbol table. go test strips a test binary this way.
	- desc: a cgo program built with -ldflags=-s still links and runs
	  cmd: export PATH="$PWD/bin:/opt/cosmocc/bin:$PATH"; out="$(mktemp -d)"; cd testdata/cgoprobe && GOCOSMOFAT=0 GOARCH=amd64 go build -ldflags=-s -o "$out/stripped.com" . && "$out/stripped.com"
	  timeout: 10m
	  exit: 0
	  outputs:
		stdout:
			- ok add
			- ok thread
			- arch amd64
		!stdout:
			- FAIL
		!stderr:
			- WinMain

	# cosmocc's own APE loader maps the arm64 payload out of the same fat APE.
	- desc: the arm64 payload of the fat APE runs under qemu
	  cmd: export PATH="$PWD/bin:/opt/cosmocc/bin:$PATH"; out="$(mktemp -d)"; cd testdata/cgoprobe && go build -o "$out/cgoprobe.com" . && qemu-aarch64 /opt/cosmocc/bin/ape-aarch64.elf "$out/cgoprobe.com"
	  timeout: 10m
	  exit: 0
	  outputs:
		stdout:
			- ok add
			- "c printf: hello 42"
			- ok printf
			- ok errno
			- ok callback
			- ok thread
			- ok concurrent
			- ok dlerror
			- arch arm64
		!stdout:
			- FAIL

	# A thin APE keeps its symbol table. Its payload starts at 64K.
	- desc: the arm64 payload carries the C code, compiled for arm64
	  cmd: |
		set -euo pipefail
		export PATH="$PWD/bin:/opt/cosmocc/bin:$PATH"
		out="$(mktemp -d)"
		cd testdata/cgoprobe
		GOCOSMOFAT=0 GOARCH=arm64 go build -o "$out/arm64.com" .
		dd if="$out/arm64.com" of="$out/arm64.elf" bs=65536 skip=1 status=none
		aarch64-linux-cosmo-readelf -h "$out/arm64.elf" | grep -E '^ +Machine: +AArch64$'
		aarch64-linux-cosmo-readelf -sW "$out/arm64.elf" | grep -E ' FUNC +GLOBAL +DEFAULT +[0-9]+ probe_add$'
		aarch64-linux-cosmo-objdump -d --disassemble=probe_add "$out/arm64.elf"
	  timeout: 10m
	  exit: 0
	  outputs:
		stdout:
			- AArch64
			- probe_add
			- "<probe_add>:"
			- ret

	# On NT libcosmo starts at WinMain, and the NT loader fills libcosmo's import tables.
	- desc: the PE header of a cgo APE enters at WinMain and names libcosmo's imports
	  cmd: |
		set -euo pipefail
		export PATH="$PWD/bin:/opt/cosmocc/bin:$PATH"
		out="$(mktemp -d)"
		cd testdata/cgoprobe
		GOCOSMOFAT=0 GOARCH=amd64 go build -o "$out/amd64.com" .
		dd if="$out/amd64.com" of="$out/amd64.elf" bs=65536 skip=1 status=none
		x86_64-linux-cosmo-objdump -p "$out/amd64.com" > "$out/pe.txt"
		grep -E 'DLL Name|CreateThread|GetProcAddress|LoadLibraryA' "$out/pe.txt"
		while read -r field value rest; do
			if [ "$field" = AddressOfEntryPoint ]; then entry="$value"; fi
			if [ "$field" = ImageBase ]; then base="$value"; fi
		done < "$out/pe.txt"
		while read -r value kind name; do
			if [ "$name" = WinMain ]; then winmain="$value"; fi
		done < <(x86_64-linux-cosmo-nm "$out/amd64.elf")
		echo "entry $entry base $base WinMain $winmain"
		if [ $((16#$entry + 16#$base)) -eq $((16#$winmain)) ]; then echo "entry is WinMain"; fi
	  timeout: 10m
	  exit: 0
	  outputs:
		stdout:
			- "DLL Name: kernel32.dll"
			- CreateThread
			- GetProcAddress
			- LoadLibraryA
			- entry is WinMain

	# cosmo satisfies the linux tag, so a Linux-only cgo file in std reaches cosmocc.
	- desc: std builds for cosmo on amd64 and arm64 with cgo on
	  cmd: export PATH="$PWD/bin:/opt/cosmocc/bin:$PATH"; GOOS=cosmo GOARCH=amd64 go build std && GOOS=cosmo GOARCH=arm64 go build std
	  timeout: 15m
	  exit: 0

	- desc: without cosmocc on PATH, cgo is off by default
	  cmd: env PATH="$PWD/bin:/usr/bin:/bin" go env CGO_ENABLED
	  exit: 0
	  outputs:
		stdout:
			0: "^0$"

	- desc: with cosmocc on PATH, cgo is on by default with a compiler per architecture
	  cmd: export PATH="$PWD/bin:/opt/cosmocc/bin:$PATH"; go env CGO_ENABLED CC; GOARCH=arm64 go env CC
	  exit: 0
	  outputs:
		stdout:
			0: "^1$"
			1: "^x86_64-unknown-cosmo-cc$"
			2: "^aarch64-unknown-cosmo-cc$"

	- desc: CGO_ENABLED=1 without cosmocc fails and names the compiler
	  cmd: out="$(mktemp -d)"; cd testdata/cgoprobe && env PATH="$PWD/../../bin:/usr/bin:/bin" CGO_ENABLED=1 GOCOSMOFAT=0 go build -o "$out/nocc.com" .
	  timeout: 10m
	  exit: 1
	  outputs:
		stderr:
			- C compiler "x86_64-unknown-cosmo-cc" not found
