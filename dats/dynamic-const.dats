# runtime.GOOS and runtime.GOARCH are variables here, so they name the
# host. A constant declaration over them still has to compile, because
# code in the wild writes one; the type checker folds the build value in
# that context only. See src/cmd/compile/internal/types2/dynconst.go.
#
# The toolchain must already be built: run make.bash first.
tests:
	- desc: a const over runtime.GOARCH compiles and reports the port
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; go run testdata/dynconst/dynconst.go
	  outputs:
		stdout:
			- "const linux/amd64"
			- "read linux/amd64"
			- "unaligned true"

	- desc: the fold follows the target rather than the host
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOARCH=arm64 go vet testdata/dynconst/dynconst.go
	  exit: 0

	- desc: vet type-checks the same declaration
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; go vet testdata/dynconst/dynconst.go
	  exit: 0
