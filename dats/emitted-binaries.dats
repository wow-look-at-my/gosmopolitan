# Every binary this toolchain emits is an APE or a wasm module. A host
# ELF, Mach-O or PE leaving a build means a port went missing, and the
# file runs on one machine instead of all of them.
#
# The toolchain must already be built: run make.bash first, or let the
# CI build leg do it.
tests:
	- desc: go build emits an APE
	  cmd: export PATH="$PWD/bin:$PATH"; go build -o "$TMPDIR/fat.com" ./testdata/fizzbuzz/fizzbuzz.go && dats/emitted-binaries.sh "$TMPDIR/fat.com"
	  exit: 0

	- desc: a thin build emits an APE too
	  cmd: export PATH="$PWD/bin:$PATH"; GOCOSMOFAT=0 GOARCH=amd64 go build -o "$TMPDIR/thin.com" ./testdata/fizzbuzz/fizzbuzz.go && dats/emitted-binaries.sh "$TMPDIR/thin.com"
	  exit: 0

	- desc: go test -c emits an APE
	  cmd: export PATH="$PWD/bin:$PATH"; go test -c -o "$TMPDIR/errors.test" errors && dats/emitted-binaries.sh "$TMPDIR/errors.test"
	  exit: 0

	- desc: the wasm ports emit wasm
	  cmd: export PATH="$PWD/bin:$PATH"; GOOS=js GOARCH=wasm go build -o "$TMPDIR/js.wasm" ./testdata/fizzbuzz/fizzbuzz.go && GOOS=wasip1 GOARCH=wasm go build -o "$TMPDIR/wasi.wasm" ./testdata/fizzbuzz/fizzbuzz.go && dats/emitted-binaries.sh "$TMPDIR/js.wasm" "$TMPDIR/wasi.wasm"
	  exit: 0

	- desc: the checker refuses a host binary
	  cmd: printf '\177ELF\002\001\001\000' > "$TMPDIR/host.elf"; dats/emitted-binaries.sh "$TMPDIR/host.elf"; test $? -eq 1
	  exit: 0
