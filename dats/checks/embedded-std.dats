# The embedded standard library: go tool embedstd writes the blob, a cosmo
# go command links it in, and with no GOROOT that command builds a program
# byte for byte as the source tree does. The toolchain must already be
# built, and cosmocc installed in /opt/cosmocc: std is built with cgo on.
tests:
	- desc: a go command carrying its standard library builds without a GOROOT what the source tree builds
	  cmd: dats/checks/embedded-std.sh
	  timeout: 25m
	  exit: 0

	# -e in the listing lets a failed package through, so the compiler check runs before any build.
	- desc: embedstd with cgo on and no cosmocc on PATH stops before any build and names the compiler
	  cmd: env PATH="$PWD/bin:/usr/bin:/bin" go tool embedstd -o "$(mktemp -d)/std.blob"
	  timeout: 2m
	  exit: 1
	  outputs:
		stderr:
			- C compiler "x86_64-unknown-cosmo-cc" is not on PATH
